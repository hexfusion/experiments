# Agentgateway + praxis picker: FQDN routing

Prep for the FQDN routing test. The goal: agentgateway forwards to an **FQDN**
destination that the praxis **picker** chooses per request, request-only, with no
hang.

## STATUS (2026-09-07, ipp-v1 tested on dagobah aigw-demo)

- **Picker_mode works.** With `ipp-v1`, the picker decides at the request-headers
  phase and picks the right FQDN per model — log: `emitting route header
  x-gateway-destination-endpoint = echo-a...:8080` for model-a, echo-b for model-b.
  The old body-wait hang is gone.
- **Live wiring fixes folded in:** agentgateway dials the ext_proc **plaintext**, but
  the picker serves self-signed TLS -> added `15-picker-backend-tls.yaml`
  (`backend.tls.insecureSkipVerify: All`). Also had to delete a stale gateway-level
  `praxis-chain` policy (grid-deep-dive-2's v14 hang experiment) that was intercepting.
- **`ipp-v2` fixed the body issue** (picker stays silent on the request body; the
  `Body() not valid for streaming mode` + stream-close is gone). Picker decides
  correctly and request-only.
- **DFP was the WRONG primitive** (dead-end for picker-driven routing). agentgateway's
  dynamicForwardProxy fixes its target from the downstream Host at route-eval and
  ignores the ext_proc output entirely (proven: it forwarded to its own addr regardless
  of `x-gateway-destination-endpoint` OR a rewritten `Host`). A generic `traffic.extProc`
  only mutates headers before forwarding to the already-chosen upstream; it never
  re-picks the endpoint.
- **The right primitive is `InferencePool` + `endpointPickerRef`** (not generic extProc):
  agentgateway's `InferencePoolRouter` (a specialized ext_proc) actually CONSUMES the
  picker's `x-gateway-destination-endpoint` and makes it the upstream. Proven live: with
  `40-inferencepool.yaml`, the router called the picker, parsed its endpoint, and tried
  to dial it — DFP never did any of that. That's the real win here.
- **FINAL VERDICT (empirically confirmed on the deployed agentgateway v1.1.0): NO native
  agentgateway picker+FQDN path.** Two live results settle it:
  - Picker emits an FQDN (`echo-a...:8080`) -> `503 EPP returned invalid address: invalid
    socket address syntax`. agentgateway parses the endpoint as a numeric Rust `SocketAddr`
    (`ext_proc.rs` `v.parse::<SocketAddr>()`), so a hostname fails in ANY mode. No DNS.
  - Picker emits an off-pool ClusterIP -> `503 no healthy backends`. v1.1.0 is
    Validated-only (XDS hardcodes `destination_mode: Validated`; no CRD/annotation), and
    Validated requires the endpoint to be a healthy pool member.
  - `destinationMode: passthrough` exists only on agentgateway `main` + only via standalone
    static config, and even there it parses `SocketAddr` — it buys OFF-POOL IP, not FQDN.
    (My earlier "passthrough = FQDN" note was a docs misread; corrected.)
- **So:** picker->upstream on agentgateway is IP-only + pool-membership (strictly LESS
  flexible than Istio ORIGINAL_DST, which takes any ip:port via `envoy.lb` metadata with
  no pool membership). DFP is the only FQDN mechanism and it's client-Host-driven, NOT
  picker-driven. External-FQDN-chosen-by-picker has no clean native path on either.
- **Decision: ride the proven Istio ORIGINAL_DST picker path** for the core
  maas-default-gateway deliverable (all in-cluster IP targets). This agentgateway
  exploration is a documented dead-end for picker+FQDN; the manifests here are kept as the
  experiment record (00/20 = the DFP dead-end; 40 = the InferencePool primitive, IP-only).

## Why this exists

- ORIGINAL_DST (the Istio path) is IP-only, so it can't cover FQDN destinations.
- agentgateway's `dynamicForwardProxy` resolves and forwards to an FQDN natively
  (PROVEN standalone: `Host: echo-a.praxis-istio-demo.svc.cluster.local:8080` ->
  200 in ~0.1s).
- Attaching the praxis picker to agentgateway used to **hang**: the v14 image is a
  full-Envoy ext_proc that holds the bidi stream for response phases, while
  agentgateway drives ext_proc as a request-only GAIE picker. `picker_mode`
  (extproc commit `51c80a9`) makes praxis answer at the request-headers phase and
  release the stream, which removes the hang.

## Blocked on

The **combined image**: grid-deep-dive-2 cherry-picks `51c80a9` (picker_mode) onto
`feat/ipp-enforce-route`, builds, pushes. Then set that tag in
`10-picker-extproc.yaml` (`REPLACE-WITH-PICKER-IMAGE`).

## Apply order

```bash
CTX=default/api-dagobah-hexfusion-local:6443/kube:admin
oc --context $CTX apply -f 00-backend-route.yaml     # DFP backend + route (already live from the standalone proof)
# edit 10-picker-extproc.yaml: set the image tag first
oc --context $CTX apply -f 10-picker-extproc.yaml     # picker in picker_mode, FQDN candidates
oc --context $CTX apply -f 20-agentgatewaypolicy.yaml # attach picker as ext_proc on the route
```

## Test

Gateway: `aigw-demo-gw` @ 192.168.1.204 (HTTP :80). Backends: echo-a / echo-b in
`praxis-istio-demo` (HTTP echo, `x-app-name: http-echo`).

```bash
# (1) isolate the picker: supply the model header directly.
#     Expect 200, NO hang, and the request landing on echo-a vs echo-b by model.
curl -sS -D - http://192.168.1.204/v1/chat/completions \
  -H "x-gateway-model-name: model-a" -H "Content-Type: application/json" \
  -d '{"model":"model-a","messages":[{"role":"user","content":"hi"}]}'

# (2) full path: model in the JSON body only (no header) -- exercises agentgateway's
#     native model-to-header promotion.
curl -sS -D - http://192.168.1.204/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"model-b","messages":[{"role":"user","content":"hi"}]}'
```

Watch the picker decide:
```bash
oc --context $CTX -n aigw-demo logs deploy/praxis-picker -f \
  | grep -iE 'model from|selected candidate|emitting route|no model header'
```

## Two open integration questions (resolve at test time)

1. **Model must be in `x-gateway-model-name`.** The picker reads the model from that
   header (it decides at the request-headers phase, before the body is parsed); real
   OpenAI clients put `model` in the JSON body. The plan is to rely on
   **agentgateway's native promotion** of the body model into that header for a GAIE
   picker. Test (1) sets the header explicitly to isolate the picker; test (2) is
   body-only. If (1) works and (2) 503s with `no model header; skipping` in the picker
   log, native promotion is not happening -- treat that as a follow-up; for the first
   routing proof, drive it with the explicit header (test 1).

2. **picker endpoint -> DFP target.** The picker emits `x-gateway-destination-endpoint`
   (the chosen FQDN:port) + `envoy.lb` metadata. DFP forwards by **Host header / SNI**.
   Whether agentgateway uses the picker's returned endpoint as the DFP target, or the
   picker must instead rewrite the request Host/`:authority`, is the unproven join. If
   requests reach the gateway but land on the wrong/no host, switch the picker to set
   the Host (or add a header rewrite) so DFP forwards to the picked FQDN.

## Notes

- Namespace: everything in `aigw-demo` (same ns as the gateway; no cross-ns
  ReferenceGrant needed). Candidates point cross-ns at echo-a/echo-b by FQDN, which
  DFP resolves cluster-wide.
- Leave `picker_mode: false` for the Istio EnvoyFilter path (it already skips
  response phases via `response_header_mode: SKIP`); `picker_mode: true` is for this
  agentgateway path.
- Not yet applied to the cluster (image pending). `00-backend-route.yaml` matches
  what's already live from the standalone DFP proof.
