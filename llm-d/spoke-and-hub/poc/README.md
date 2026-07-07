# Multi-cluster load-aware routing (hub + spokes)

A hub cluster routes each inference request to the **least-loaded spoke** and injects that spoke's
MaaS token. Proof-of-concept manifests; spokes run stock RHOAI 3.4 serving.

## Layout

- `hub/` — cluster-EPP (picks the spoke by load), IPP (injects the per-spoke token), gateway,
  ORIGINAL_DST forward, GIE CRDs.
- `spokes/` — `epp-metrics-mtls`: a LoadBalancer exposing the spoke EPP `:9090` aggregate over mTLS, plus
  the LLMISvc override that swaps in the mTLS EPP image. The hub scrapes `queue_size` +
  `kv_cache_utilization` per spoke with a client cert (no proxy, no token).

## Apply

Each spoke (create the cert Secrets first — see Env-specific — then):
```sh
kubectl apply -f spokes/epp-metrics-mtls.yaml    # LB; then apply the LLMISvc override patch in that file
```
Hub:
```sh
kubectl apply -f hub/gie-inferencepool-crd.yaml -f hub/gie-inferencepool-xk8s-crd.yaml
kubectl apply -f hub/gatewayclass.yaml -f hub/hub-gateway.yaml -f hub/hub-pool.yaml
kubectl apply -f hub/cluster-epp.yaml -f hub/ipp.yaml
kubectl apply -f hub/ipp-envoyfilter.yaml -f hub/original-dst.yaml
```

## Example request

Client sends **no auth header** (the hub injects the spoke token). HTTPS on `443` (the POC uses a
self-signed cert, hence `-k`); model id `Qwen/Qwen3-1.7B` (the HF served name, not `qwen3-1-7b`, which 404s).

```sh
GW=$(kubectl get gateway hub -n aig-routing -o jsonpath='{.status.addresses[0].value}')

curl -k https://$GW/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{
    "model": "Qwen/Qwen3-1.7B",
    "messages": [{"role": "user", "content": "Say hello in one short sentence."}],
    "max_tokens": 32
  }'
# -> HTTP 200, chat completion from Qwen/Qwen3-1.7B
```

## Env-specific (not baked into the manifests)

- **EPP image** (hub + spoke): custom `llm-d-router` build `mtls-fqdn-v5` adding `metricsAddress`, client
  mTLS, and metrics-server mTLS (upstream PRs https://github.com/llm-d/llm-d-router/pull/1857 and
  https://github.com/llm-d/llm-d-router/pull/1858, plus a metrics-server-mTLS PR).
- **IPP plugin**: `destination-provider-resolver` (upstream PR compare
  https://github.com/opendatahub-io/ai-gateway-payload-processing/compare/main...hexfusion:destination-keyed-credential).
- **mTLS certs** (one CA both roles trust): spoke `aig-epp-server-cert` (SAN = the EPP metrics-LB FQDN) +
  `aig-hub-ca`; hub `cluster-epp-metrics-client`. `cluster-epp-config` needs a `spoke-ca.pem` key = each
  spoke's EPP **server** CA concatenated.
- Per-spoke `ExternalProvider` + `bbr-managed` Secret (the `sk-oai` token) for the IPP.
- Spoke maas ELB IPs (forward) + EPP metrics-LB FQDNs (mTLS scrape) are in `hub/cluster-epp.yaml`; update per fleet.

Full detail + reproduce steps: `hexfusion/design` under `work/llm-d/ai-grid/deployment/`.
