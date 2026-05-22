# praxis-gateway

Off-series experiment. Puts a single Praxis (Rust/Pingora) binary in front of
vLLM and exercises the in-process filter chain end-to-end. Same
`llm-d-inference-sim` backend pool as `02-vllm-sim` for the local variant;
the cluster variant runs against real Qwen2.5-7B-AWQ on a T4.

Purpose: produce a concrete runnable artifact for the Rust-gateway
direction conversation. Validates what an in-process Rust filter pipeline
at L7 actually does, surfaces what it cannot do without an out-of-band
state service, and quantifies the basic plumbing overhead.

This experiment is **not** an IPP-shaped deployment in the F2F whiteboard
sense (no auth/rate/guardrails plugin chain, no Coordinator handoff, no
tenant-header emission). It's a thin reverse proxy. If/when a real
IPP-shaped Praxis experiment is built, it would live alongside this one.

## What this demonstrates

- Praxis (pinned to upstream `v0.3.1`) terminates HTTP at `:8080` and runs the configured filter chain inline.
- `model_to_header` filter peeks the JSON request body, extracts the `model` field, promotes it to an `X-Model` header. Replaces the `bbr-mock` ext_proc service from 02.
- `router` + `load_balancer` (consistent-hash on `X-Model`) routes to a backend pool.
- Hot-reload of `praxis/config.yaml` swaps the filter pipeline atomically via Praxis's `ArcSwap` mechanism.

## What this deliberately does NOT do

These gaps are the point of the experiment, not bugs:

- **No prefix-cache-aware routing.** Praxis v0.3.1 ships no scorer that reads vLLM KVEvents. The 02 demo's `llm-d-kv-cache` indexer integration has no analog here. Routing is consistent-hash on the model header, not cache-aware.
- **No EPP scorer pipeline.** No Filter/Score/Pick stages, no `KVCacheScorer` / `LoadAwareScorer` / `PdProfileHandler` equivalents.
- **No GIE / `InferencePool` consumption.** Cluster membership is static YAML, not Gateway API CRDs.
- **No P/D disaggregation awareness.** Praxis cannot distinguish prefill from decode pools.

These gaps map to what would need to be built (in Rust filters, or as out-of-band services Praxis calls into) for Praxis to replace agentgateway + EPP for real.

## Components

| Real | Mocked |
|---|---|
| Praxis `v0.3.1` (built from upstream) | — |
| **Local variant:** `llm-d-inference-sim` × 3 (`ghcr.io/llm-d/llm-d-inference-sim:v0.8.0`) | — |
| **Cluster variant:** real vLLM serving Qwen2.5-7B-Instruct-AWQ on a T4 | — |

## Local variant — podman compose

```
make praxis-gateway-up      # podman compose up --build -d
make praxis-gateway-test    # exercise model_to_header + LB
make praxis-gateway-down    # podman compose down
```

`test.sh` sends two model values (`qwen-7b-awq`, `mistral-7b-instruct`) and checks that consistent-hash routes them deterministically to different pod subsets.

- `localhost:8080` — Praxis ingress

## Trace shape

```
praxis: ingress
  ├─ filter: model_to_header (body peek, header promotion)
  ├─ filter: router (match X-Model → cluster)
  ├─ filter: load_balancer (consistent-hash on X-Model)
  └─ upstream → vllm-sim-N (or vllm)
```

No ext_proc hops. No cross-process IPC. Every filter stage is a function call inside the Praxis process.

## Cluster variant — dagobah OCP, real GPU

`cluster/` deploys praxis + one real vLLM pod (Qwen2.5-7B-Instruct-AWQ) on a T4 worker. Single Praxis pod proxies it. Same filter chain as the local variant; differences are (a) endpoint resolves via in-cluster Service DNS, (b) test runs through an OCP Route or `kubectl port-forward`.

**Prerequisites (one-time):**

- `oc login` to dagobah (`https://api.dagobah.hexfusion.local:6443`).
- `~/.config/hf-token` containing your HF token (used by `scripts/ensure-hf-token.sh`).
- Praxis image built and pushed to the OCP integrated registry (no public image exists for v0.3.1):
  ```
  ./cluster/scripts/build-praxis.sh
  ```
  The script logs in to the integrated registry using a service-account token from the target namespace; no quay or external registry needed.

**Run:**

```
./cluster/scripts/up.sh         # apply manifests, wait for rollout
./cluster/scripts/test.sh       # 5 reqs via Route (falls back to port-forward)
./cluster/scripts/down.sh       # delete namespace
```

`up.sh` handles namespace creation, HF token secret, manifest apply, and rollout wait.

**Target node:** `cluster/manifests/20-vllm.yaml` pins to `endor.dagobah.hexfusion.local` (bare-metal T4). Worker4/worker5 currently expose `nvidia.com/gpu` capacity 0 in K8s, so endor is the only working target as of 2026-05-22.

**What this lets you measure that the sim variant cannot:**

- Real TTFT through Praxis to a real GPU prefill.
- Real SSE streaming behavior on long-running completions.
- Whether Praxis's StreamBuffer body-mode interferes with vLLM's streaming response on the way back.
- End-to-end overhead of the filter chain on real inference latency (compare to direct curl against vllm Service).

## Findings

See [`findings.md`](./findings.md) for first-run observations (2026-05-22):
real-GPU end-to-end working; ~290ms warm latency through Praxis vs ~270ms direct; bugs surfaced (K8s service-link `VLLM_PORT` collision, `--locked` vs workspace stripping in build, observability gaps in access_log).

## Related

- `../02-vllm-sim/` — Envoy + ext_proc + EPP + indexer baseline.
- `~/projects/hexfusion/design/work/llm-d/praxis/RESEARCH.md` — assessment that motivated this experiment.
- `~/projects/praxis-proxy/praxis/` — local clone of the upstream project.
