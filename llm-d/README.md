# llm-d-stack

Progressive demos of the llm-d AI Gateway architecture. Each numbered directory is a runnable stack that adds one architectural concept on top of the previous.

## Demos

| # | Dir | Adds | Shell | Status |
|---|---|---|---|---|
| 01 | `01-basic/` | Envoy + ext_proc filter chain + Jaeger | podman | working |
| 02 | `02-vllm-sim/` | Real `llm-d-inference-sim` + in-process `llm-d-kv-cache` indexer | podman | working |
| 03 | `03-kind-raw/` | Real upstream EPP + Gateway API + `InferencePool` + agentgateway | kind | working |
| 04 | `04-multimodal-raw/` | Lab T4, real LLaVA / Qwen2-VL, raw `Deployment` + LWS | kind | planned |
| 05 | `05-kserve/` | KServe `LLMInferenceService` | kind | planned |
| 06 | `06-rhoai/` | Full RHOAI bundle on dagobah | RHOAI | planned |

## Run

```
make                  # build every binary into <component>/bin/
make verify           # fast guardrail: go build + script syntax + manifest/compose validation (~18s, no cluster)
make 01-up            # bring demo NN up   (01 / 02 = podman compose; 03 = kind)
make 01-test          # run the demo's test scenario
make 01-down          # tear demo NN down
```

Each demo has its own `README.md`. Jaeger UI at <http://localhost:16686>.
