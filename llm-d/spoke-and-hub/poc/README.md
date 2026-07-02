# Multi-cluster load-aware routing (hub + spokes)

A hub cluster routes each inference request to the **least-loaded spoke** and injects that spoke's
MaaS token. Proof-of-concept manifests; spokes run stock RHOAI 3.4 serving.

## Layout

- `hub/` — cluster-EPP (picks the spoke by load), IPP (injects the per-spoke token), gateway,
  ORIGINAL_DST forward, GIE CRDs.
- `spokes/` — metrics-gateway proxy: exposes the spoke EPP `:9090` aggregate (token-injected) so the
  hub can scrape `queue_size` + `kv_cache_utilization` per spoke.

## Apply

Each spoke:
```sh
kubectl apply -f spokes/metrics-gateway.yaml
```
Hub:
```sh
kubectl apply -f hub/gie-inferencepool-crd.yaml -f hub/gie-inferencepool-xk8s-crd.yaml
kubectl apply -f hub/gatewayclass.yaml -f hub/hub-gateway.yaml -f hub/hub-pool.yaml
kubectl apply -f hub/cluster-epp.yaml -f hub/ipp.yaml
kubectl apply -f hub/ipp-envoyfilter.yaml -f hub/original-dst.yaml
```

## Example request

Client sends **no auth header** (the hub injects the spoke token). Port `8080`; model id
`Qwen/Qwen3-1.7B` (the HF served name, not `qwen3-1-7b`, which 404s). Get the gateway host from
`kubectl get gateway hub -n aig-routing -o jsonpath='{.status.addresses[0].value}'`.

```sh
curl http://<hub-gateway>:8080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{
    "model": "Qwen/Qwen3-1.7B",
    "messages": [{"role": "user", "content": "Say hello in one short sentence."}],
    "max_tokens": 32
  }'
# -> HTTP 200, chat completion from Qwen/Qwen3-1.7B
```

## Env-specific (not baked into the manifests)

- **Hub EPP image**: custom `llm-d-router` build adding `metricsAddress` + `caCertPath` (upstream PRs
  https://github.com/llm-d/llm-d-router/pull/1857 and https://github.com/llm-d/llm-d-router/pull/1858).
- **IPP plugin**: `destination-provider-resolver` (upstream PR compare
  https://github.com/opendatahub-io/ai-gateway-payload-processing/compare/main...hexfusion:destination-keyed-credential).
- `cluster-epp-config` needs a `spoke-ca.pem` key = each spoke's ingress CA concatenated.
- Per-spoke `ExternalProvider` + `bbr-managed` Secret (the `sk-oai` token) for the IPP.
- Spoke ELB IPs + metrics-gateway Route hosts are hardcoded in `hub/cluster-epp.yaml`; update per fleet.

Full detail + reproduce steps: `hexfusion/design` under `work/llm-d/ai-grid/deployment/`.
