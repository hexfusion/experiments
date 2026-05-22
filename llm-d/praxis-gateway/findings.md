# Findings — praxis-gateway

First-run observations from the cluster variant on dagobah OCP, vLLM 0.21.0 serving Qwen2.5-7B-Instruct-AWQ on endor (T4 bare-metal), Praxis 0.3.1 in front.

Date: 2026-05-22.

## Did it work

Yes. End-to-end functional. Five completion requests (model=qwen-7b-awq, prompt=Paris/Madrid/Italy, max_tokens=5) all returned HTTP 200 through the Praxis filter chain to vLLM on a real T4.

```
req 1 status=200 latency=2388ms     # cold path: Triton JIT compile (per vLLM log)
req 2 status=200 latency=306ms
req 3 status=200 latency=292ms
req 4 status=200 latency=291ms
req 5 status=200 latency=284ms      # warmed
```

Direct-to-vLLM baseline (same prompt, same Service, no Praxis):

```
~270ms / request (warm)
```

**Gateway overhead: ~20–30ms.** This is consistent with the in-process filter chain doing body peek + header promotion + router match + consistent-hash LB on a TLS-terminated Route → ClusterIP path. No ext_proc round-trips in this path.

## Bugs / Gotchas surfaced

1. **K8s service-link env-var injection collides with `VLLM_PORT`.**
   The Service named `vllm` causes the kubelet to inject `VLLM_PORT=tcp://172.30.237.54:8000` into pods in the same namespace. vLLM 0.21.0's own `VLLM_PORT` env var expects an integer; it fails fast with:

   ```
   ValueError: VLLM_PORT 'tcp://172.30.237.54:8000' appears to be a URI.
   ```

   Fix: `enableServiceLinks: false` on the pod spec (in `20-vllm.yaml`). Worth noting because this is a clean collision: any vLLM pod whose Service name is `vllm` will hit this on any K8s. Service rename works too but is uglier because every consumer (praxis config endpoints) has to know the alternate name.

2. **Praxis fork pin requires `--locked` removal.**
   Upstream Praxis 0.3.1's Cargo.lock references workspace members (xtask, benchmarks, tests) that the experiment Containerfile strips for build size. `cargo build --release --locked` then refuses to rewrite the lockfile. Dropped `--locked` from `praxis/Containerfile` — pinning is enforced by the upstream tag + the pingora fork SHA in Cargo.toml anyway.

3. **`quay.io:443/sbatsche` robot account can't create new repos.**
   The `sbatsche+dev` robot has push permission to *existing* repos but not create. Pushing to a new `sbatsche/praxis` repo failed with `authentication required` on the first blob upload. Switched to the OCP integrated registry (`image-registry.openshift-image-registry.svc:5000/llm-d-praxis/praxis`); in-namespace pods authenticate via SA token, no pull secret needed. Build script updated.

4. **`rollout restart` after `apply` produces a phantom ReplicaSet.**
   Doing `kubectl apply -f 20-vllm.yaml` + `kubectl rollout restart deploy/vllm` in sequence created two ReplicaSets at the new generation, both desired=1, fighting for the single T4. Symptom: rollout status hangs with "1 old replicas are pending termination" forever. Fix: just apply once. The `up.sh` script does this correctly; the mid-debug restart caused it.

## Architectural observations

The whole point of this experiment was to put the Praxis-as-IPP hypothesis against a real GPU. What we learned:

- **The in-process filter chain works exactly as advertised.** model_to_header (StreamBuffer body peek), router header match, consistent-hash LB, access_log — all stages exercised inline with no ext_proc round-trip. No surprises in the per-request flow. Warm latency is ~20-30ms above direct.

- **Praxis observability does not expose which upstream was picked.** The access_log filter emits `upstream="-"` for every request even though the request was successfully routed. Praxis has no analog of Envoy's `%UPSTREAM_HOST%` response-header injection. For a one-backend cluster this is moot; for a multi-backend cluster (where you'd actually care about consistent-hash routing decisions) this would be a real gap. To prove a pod-pinning claim you'd have to read access logs from each backend, not from Praxis. This is consistent with what RESEARCH.md predicted.

  Sample access log line (note the `upstream="-"`):
  ```
  status=200 duration_ms=286 cluster="vllm" upstream="-" request_id="..."
  ```

- **`response_body_bytes=0` in Praxis access log.** Either Praxis isn't measuring response body size on streaming responses, or StreamBuffer's response-side path discards the count. Didn't dig in. Worth filing upstream if confirmed.

- **No prefix-cache-aware routing was attempted.** Praxis has no scorer that subscribes to vLLM KVEvents. The shape of the filter chain (`router` → `load_balancer` consistent-hash) is exactly what you'd reach for if you wanted prefix affinity, but the hash key is the model header, not the prompt prefix. To get prefix-cache awareness you'd need either (a) a Praxis scorer that opens a streaming connection to each backend's cache-event source and updates an in-proxy radix tree, or (b) an external state service that Praxis calls per request. Both are the architectural moves the RESEARCH.md doc flagged as the structural problem with "everything is a filter at L7." Nothing about this experiment changed that analysis; it just confirmed Praxis itself doesn't bridge the gap today.

- **Cold-start tax was 2.1s** for the first request after vLLM came Ready. vLLM logged `Triton kernel JIT compilation during inference: _compute_slot_mapping_kernel` — this is a known vLLM warmup issue independent of Praxis. Mention here because anyone trying to compare gateway shapes on TTFT needs to discard the first request.

## What's set up that we can build on

- `cluster/` manifests are clean and applicable from scratch — no manual cluster prep beyond `oc login` + `~/.config/hf-token`.
- Praxis image lives in OCP integrated registry. Rebuilding is `./cluster/scripts/build-praxis.sh`; pushes are SA-authenticated.
- Route at `https://praxis-llm-d-praxis.apps.dagobah.hexfusion.local` exposes the gateway externally with edge TLS.
- vLLM Service is `vllm.llm-d-praxis.svc:8000` in-cluster (or via port-forward).

## What this doesn't yet exercise

Worth listing because the experiment as it stands only validates the basic filter-chain shape. Next moves, in rough order of cost-to-value:

1. **Direct-vs-Praxis side-by-side latency under load** — sustained RPS via a real benchmark tool (k6, vegeta) rather than 5 curls. Measure p50/p95/p99 overhead at various concurrencies. Easy follow-up; same manifests.

2. **SSE streaming through StreamBuffer** — current test uses non-streaming completions. The interesting case is whether StreamBuffer's body-peek mode interferes with vLLM's SSE streaming on the way back. Run with `stream: true` and watch for buffering surprises.

3. **Two backends with two models** — scale vLLM Deployment to 2 replicas (requires freeing a second GPU; worker4/5 currently show capacity=0), serve two `--served-model-name` values, validate that `model_to_header` + consistent-hash routes them to different pods. This is what the README claims; we haven't actually proven it on real backends.

4. **Multi-tenancy worked example** — drop the routing question and exercise the second-order concern from RESEARCH.md: does one tenant's body-mode body-peek burn worker CPU that another tenant pays for. Probably needs a real fortio run.

5. **Prefix-cache-aware filter prototype** — write a Praxis HttpFilter that subscribes to vLLM's KV events (over the same ZMQ topic the llm-d-kv-cache indexer consumes) and updates an in-proxy radix tree. This is the real architectural experiment — it would expose exactly how much of the indexer has to be reimplemented inside the proxy for "cache-aware at L7" to work. Multi-week project, not next-session.

## Cluster cleanup

The deploy used 1 T4 on endor. Other lab resources:
- worker4 / worker5: `nvidia.com/gpu` capacity=0 in K8s (T4s not currently bound to those VMs).
- llm-d-test, vllm-37581 namespaces: untouched; not GPU-blocking.

To free the T4: `./cluster/scripts/down.sh` (deletes the whole namespace).
