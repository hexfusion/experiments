# 03-kind-raw

First demo on a real Kubernetes API. Drops `epp-mock` for the upstream EPP (`llm-d-inference-scheduler`) and replaces the static Envoy config with Gateway API + `InferencePool` + agentgateway.

## Components

| Real | Mocked |
|---|---|
| kind cluster, Gateway API + GIE CRDs | `bbr-mock` |
| agentgateway v1.1.0 (controller + data plane) | |
| `llm-d-inference-scheduler:v0.8.0` (EPP, with `precise-prefix-cache-scorer`) | |
| UDS tokenizer sidecar (TinyLlama HF tokenizer, pre-baked) | |
| `llm-d-inference-sim` × 3 | |
| Jaeger all-in-one (dark CSS injected via nginx sidecar) | |

## Run

```
make 03-up        # kind create + CRDs + agentgateway + manifests + port-forward
make 03-test      # warm-up + same-prefix + unique-prefix
make 03-down      # kind delete
```

(All three are thin wrappers over `scripts/setup.sh` / `test.sh` / `teardown.sh`.)

`make 03-up` may prompt for sudo to raise inotify limits and load `ip_tables` kernel modules (kind + rootless podman requirements).

- `localhost:18080` — Gateway data plane (via `kubectl port-forward`)
- `localhost:16686` — Jaeger UI

## Trace shape

```
gateway.request
  └─ gateway.request_orchestration
      ├─ gateway.scheduling.filter      (decode-filter)
      ├─ gateway.scheduling.scorer      (precise-prefix-cache-scorer)
      │   └─ llm_d.epp.scorer.prefix_cache
      │       └─ llm_d.kv_cache.score_tokens
      └─ gateway.scheduling.picker      (max-score-picker)
```

`gateway.scheduling.*` spans require the per-plugin-spans patch on `llm-d-inference-scheduler` — see `work/llm-d/proposals/XXXX-epp-per-plugin-spans/`.

## Known gaps

- **agentgateway → EPP propagation:** v1.1.0 doesn't inject `traceparent` into ext_proc gRPC metadata, so the gateway span and the EPP trace are disconnected.
- **vllm-sim → no spans:** sim v0.8.0 has no OTel instrumentation; the upstream HTTP hop is invisible.
