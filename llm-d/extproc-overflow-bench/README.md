# extproc-overflow-bench

Measures held ext_proc stream capacity and the binding resource on real Envoy
data planes (Envoy Gateway, kgateway, Istio): where one Envoy + one EPP stops
scaling, and whether the connection-buffer breaker is a tunable knob or a wall.
Full derivation: `MODEL.md`.

## Formula

```
C_extproc = W × min(R, K×S)         max concurrent ext_proc streams (Envoy CB defaults 1024)
overflow ⇔ λ × E[T_hold] > C_extproc
gain(B/A) = E[T_response] / E[T_route]   ≈ 10³–10⁴×
```
Mode A (`response_body_mode: SEND`) holds the stream for the whole SSE response
→ ext_proc is the bottleneck. Mode B (`NONE`) releases it after routing → it
isn't. Same Envoy fields across all three gateways; only the CRD differs.

## Prereqs

- One gateway installed (Envoy Gateway / kgateway / Istio) + GIE CRDs.
- EPP + InferencePool from the GIE `inferencepool` helm chart, `provider.name`
  per gateway, selector `app=sim`, targetPort 8000.
- TLS is production-like: HTTPS listener (cert-manager Secret `inference-gateway-tls`),
  mTLS/verified TLS gateway→EPP. No skip-verify. (TLS CPU is part of the A2 ceiling.)

## Run (Makefile, one gateway at a time)

```
make up GW=istio                     # ns, sim, gateway, EPP(+flag), knobs, procmode, podmonitor
make scale GW=istio SIM_REPLICAS=20  # backend not the bottleneck
make stats GW=istio CLUSTER=<c> GW_SELECTOR=<l>   # both scrapers (port-forward admin first)

make a1 GW=istio                     # breaker knee, single-box ramp 512..4096
# A2: set R/K/S=1000000 in gateways/istio/knobs.yaml, re-apply, then big ramp:
make scale && make bigramp GW=istio  # distributed 2k→50k via k6-operator
# B: set response_body_mode=NONE in gateways/istio/procmode.yaml, re-apply, re-ramp
make summarize                       # results/ → per-arm + formula
make down GW=istio                   # then next gateway
```

Notes: **A1 fails at ≈1024×W** (Envoy workers) by design — that's the breaker
knee, single-box is enough. The 2k→50k push is A2/B only and needs **distributed
k6** (`bigramp`, parallelism shards source IPs past one IP's ~28k port limit; needs
grafana/k6-operator). Raise **Envoy `--concurrency` + pod CPU** to lift `W`
(istio: `proxy.istio.io/config: '{"concurrency":N}'`; Envoy Gateway: `EnvoyProxy`
CRD) — else A2/B caps at `1024×W` regardless of offered load.

## Observe

`kubectl apply -f dashboards/podmonitor.yaml` (set ports per gateway), import
`dashboards/extproc-grpc.json` into Grafana. Watch `rq_active` vs the 1024 line,
`rq_pending_overflow`, the breaker-open stat, and gateway CPU (A2 binding signal).

## Criteria

| Arm | Pass condition |
|---|---|
| **A1** | 500s appear at concurrency ≈ `W×1024`, `upstream_rq_pending_overflow`↑ + `rq_open=1`, gateway CPU **below** limit → breaker-bound |
| **A2** | overflow clears; new ceiling reached; binding resource named (CPU%/FDs/mem) → breaker is a tunable knob, not a wall |
| **B** | zero overflow at max concurrency; gain vs A1 tracks `E[T_response]/E[T_route]` → symptom is a processing-mode choice |

Conclusion is product-shape only (lab-free); raw runs in `results/`, written up
in `findings.md`.
