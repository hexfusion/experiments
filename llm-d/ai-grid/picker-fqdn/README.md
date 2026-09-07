# Picker-driven FQDN forwarding (DNS + SNI)

The praxis picker chooses an upstream **per request**, and the gateway forwards to
that **FQDN** with real DNS resolution (and TLS SNI for external HTTPS). Proven
working on **both** a standalone Envoy and the OpenShift-managed **Istio** gateway.

This supersedes and replaces the earlier `agentgateway-picker/` and `picker-fqdn-dfp/`
explorations (both removed) — their conclusions were wrong; see the root cause below.

## The mechanism

```
client -> gateway
  -> ext_proc (praxis picker, picker_mode, REQUEST-ONLY):
       picks the candidate for the model, emits the chosen FQDN as
       envoy.lb dynamic_metadata[x-gateway-destination-endpoint]
  -> header_mutation filter (native, no Lua):
       x-chosen-host = %DYNAMIC_METADATA(["envoy.lb","x-gateway-destination-endpoint"])%
  -> dynamic_forward_proxy filter (host_rewrite_header: x-chosen-host):
       DNS-resolves the FQDN (+ auto-SNI on a TLS cluster) and forwards
```

Filter order MUST be `ext_proc -> header_mutation -> dynamic_forward_proxy -> router`,
and the DFP **filter and cluster must share one `dns_cache_config`**.

## The root cause we fought (read this)

Picker-driven FQDN via `host_rewrite_header` reading a praxis-SET header does NOT
work, and it took a long time to find why. It is **not** the gateway, **not** Istio
stripping, **not** the Envoy version / CVE. It is a **praxis-extproc bug**:

- Envoy's ext_proc applies `set_headers` from `HeaderValue.raw_value`
  (`source/extensions/filters/http/ext_proc/mutation_utils.cc:156`, v1.33 and v1.34).
- praxis-extproc `adapter.rs::header_value_option` sets only `HeaderValue.value`
  (never `raw_value`, intentionally, per its own comment).
- So **every praxis ext_proc header mutation is applied to the request with an EMPTY
  value** for downstream Envoy filters. This is masked when routing via
  dynamic_metadata (ORIGINAL_DST reads envoy.lb metadata, set directly) but breaks any
  path that needs a praxis-set header downstream (host_rewrite_header, header-route-match).

Two fixes:
1. **Proper fix (praxis-extproc image):** in `adapter.rs::header_value_option` set
   `raw_value` instead of `value` (and update `destination_metadata` / header readers to
   read `raw_value`). Then praxis headers apply correctly downstream and you can use
   `host_rewrite_header: <praxis header>` directly — no header_mutation bridge needed.
2. **No-rebuild workaround (what's configured here):** route the destination through the
   RELIABLE `dynamic_metadata` path and bridge it to a header with the native
   `header_mutation` filter + `%DYNAMIC_METADATA%` formatter. No praxis rebuild.

## Other hard-won facts

- **DNS: use a trailing-dot FQDN** (`echo-a...svc.cluster.local.:8080`). Envoy's c-ares
  resolver + the pod's `ndots:5` fails the search-domain expansion; the trailing dot
  forces an absolute lookup. Without it: `503 DNS resolution failure`.
- **ext_proc must be request-only** (`request_body_mode: NONE`, response modes SKIP) so
  the picker's header-phase `dynamic_metadata` is available before the header_mutation
  filter runs. With BUFFERED, the decision defers to the body phase and the bridge misses it.
- **CVE-2025-54588 is a non-issue here**: this pattern rewrites Host AT the DFP filter,
  not between DFP and Router, so `dfp_cluster_resolves_hosts=true` (default) does not crash.
- **External HTTPS (site-c openrouter, anthropic, etc.):** add a TLS `transport_socket`
  (`UpstreamTlsContext`) to `dfp_cluster` for auto-SNI to the resolved host. In-cluster
  plaintext (echo, site-a/b workload svcs) needs no TLS. (Cluster egress to external HTTPS
  verified working on dagobah.)

## Files

- `raw-envoy/` — standalone Envoy PoC (we own the whole config). `poc.yaml` is the full
  all-in-one (ns + picker + envoy); `envoy.yaml` / `picker-extproc.yaml` are the pieces.
  Proven: model-a->echo-a, model-b->echo-b, 200, via DFP DNS resolution.
- `istio/` — the same mechanism on the managed `praxis-istio-demo-gw`.
  `envoyfilter.yaml` injects ext_proc(+metadata_options) -> header_mutation -> DFP + the
  route MERGE (host_rewrite_header) onto Istio's native catch-all route;
  `picker-extproc-config.yaml` is the picker (picker_mode, route_header
  x-gateway-destination-endpoint, FQDN candidates). Proven: model-a->echo-a,
  model-b->echo-b, 200.

## Backends

Uses the `echo-a`/`echo-b` http-echo services in the `praxis-istio-demo` namespace as
stand-in FQDN targets. Swap the picker candidates for real site FQDNs
(qwen3-kserve-workload-svc.ai-tenant-site-a..., etc.) to route to live vLLM.

## Body-model variant (frontdoor-compatible) — the important one

The demo above reads the model from a client HEADER (`x-gateway-model-name`), so the
picker decides at the request-headers phase. Real OpenAI/frontdoor clients put the model
in the JSON BODY. That ALSO works, with no separate BBR and no picker_mode:

- extproc chain: `json_body_field(field=model, header=X-Model)` -> `intelligent_route(model_header=X-Model, route_header=x-gateway-destination-endpoint, FQDN candidates)`, `request_body_mode: buffered`.
- ext_proc filter `processing_mode.request_body_mode: BUFFERED`.

Why the timing works: with BUFFERED, the ext_proc filter HOLDS the filter chain
(StopIterationAndBuffer) until the body is read and the pipeline runs. So the body-phase
routing decision + envoy.lb metadata are set BEFORE the downstream header_mutation and DFP
filters run. `json_body_field` extracts the model NAME from the body and injects X-Model;
the picker keys on it; the metadata carries the chosen FQDN; header_mutation bridges it to
x-chosen-host; DFP resolves. Proven: model-a->echo-a, model-b->echo-b, 200, with the model
in the body only (no client header).

Files: `istio/picker-extproc-config-bodymodel.yaml` (the body-model picker chain) + the
same `istio/envoyfilter.yaml` (ext_proc now BUFFERED). This is the pattern to put on the
grid frontdoor, which already uses json_body_field to promote the model from the body —
add metadata_options + the header_mutation filter + DFP + route MERGE, on the ipp-v2 image
(v12 does not emit the envoy.lb metadata). Keep the frontdoor's auth/rate-limit/token_count.
