# Reproducing the live demo

This package plus the steps below reproduce the dagobah grid demo as it ran on 2026-09-08. Read the dependency chain first: several settings are coupled and must flip together or the stack half-works silently.

## The TLS toggle and its dependency chain (out-of-band bootstrap)

The demo runs site-a/site-b vLLM with plaintext `:8000`. That is driven by one global KServe setting that is NOT expressed in this package (it lives in a cluster ConfigMap), so apply it as a bootstrap step, do not rely on it silently.

Set `enableLLMInferenceServiceTLS=false` on the KServe global config:

```
# 1. stop odh-model-controller reverting the ConfigMap
oc annotate cm inferenceservice-config -n redhat-ods-applications opendatahub.io/managed=false --overwrite

# 2. flip the single ingress key, preserving every other key (the value is a JSON string).
#    Live full value is captured in the drift dump live/isvc-config.json; the only change is
#    "enableLLMInferenceServiceTLS": false. Patch the whole ingress string back with that key set:
oc patch cm inferenceservice-config -n redhat-ods-applications --type=merge -p \
  '{"data":{"ingress":"{\"disableIngressCreation\": true, \"disableIstioVirtualHost\": false, \"domainTemplate\": \"{{ .Name }}-{{ .Namespace }}.{{ .IngressDomain }}\", \"enableGatewayApi\": false, \"enableLLMInferenceServiceTLS\": false, \"ingressClassName\": \"openshift-default\", \"ingressDomain\": \"example.com\", \"ingressGateway\": \"knative-serving/knative-ingress-gateway\", \"ingressService\": \"istio-ingressgateway.istio-system.svc.cluster.local\", \"knativeLocalGatewayService\": \"knative-local-gateway.istio-system.svc.cluster.local\", \"kserveIngressGateway\": \"openshift-ingress/openshift-ai-inference\", \"localGateway\": \"istio-system/kserve-local-gateway\", \"localGatewayService\": \"kserve-local-gateway.istio-system.svc.cluster.local\", \"urlScheme\": \"http\"}"}}'
```

Setting `enableLLMInferenceServiceTLS=false` makes a/b vLLM `:8000` plaintext, which REQUIRES all of the following to be plaintext/absent too. Flip the toggle and all four follow. Never half-apply:

- grid-prometheus site-a and site-b jobs must be `scheme: http` (this package, `observability/grid-prometheus/grid-prometheus.yaml`).
- the KServe-generated DestinationRules that force upstream TLS must be absent.
- KServe readiness/liveness probes must be HTTP (this package pins an explicit HTTP `livenessProbe` on the vLLM container in `vllm/serving.yaml`).

If any one stays on the TLS assumption while vLLM is plaintext (or vice versa), scrapes and probes fail quietly and routing signals go stale.

This is a demo posture, not production. It drops transport security between the hub and the model servers.

## Demo beats

Four beats, run against the grid frontdoor. One line each on what each proves.

- `grid-perf load 32 45` — 32 concurrent streams for 45s. Proves load distributes across sites (roughly a/b/d in proportion to declared order and live load), not mono-routing to one site.
- `geo-us` | `geo-eu` | `geo-uk` | `geo-fail` — mints/uses region-scoped tokens and shows geo-fencing by the signed `grid_region` claim: US stays on a/b, EU/UK reach the fenced region, and `geo-fail` proves the fence fails closed for a region with no eligible site.
- `context` — sends a request that exceeds a site's context window and proves the context-window filter rejects/reroutes (413) rather than truncating.
- `token-fast` / `token-sustained` — drives token-metered rate limiting: `token-fast` bursts to trip the token quota (429), `token-sustained` holds a sustained rate to show the limiter refilling and admitting under the cap.

## Reproduction gotchas

- At IDLE all traffic correctly goes to site-b (all sites read 0, ties break to declared order). Distribution requires concurrent load. Do not validate routing with a few curls; use `grid-perf load` and read the split.
- The region-less `DEMO` / `SUSTAINED_DEMO` / `CAPPED_DEMO` tokens carry no `grid_region` claim. On ipp-v3.8 that is fine: site-c is skipped as an unscored candidate, so it cannot poison the group, and these tokens distribute across a/b/d. site-c is reachable ONLY via the `CHRIS_UK` token (region `eu-west-2`).

## Demo-shortcuts to revert post-demo

These are demo postures, not production. Back them out after recording:

- site-d EPP `--metrics-endpoint-auth=false` plus the plain-http NodePort `30091` (`router/site-d/router.values.yaml` `router.epp.flags` and `router/site-d/epp-metrics-nodeport.yaml`). Re-enable EPP metrics auth and delete the NodePort.
- grid-prometheus site-a/site-b `scheme: http` (tied to the `enableLLMInferenceServiceTLS=false` toggle above). Flip back together with the toggle, never alone.
- the readiness-probe flap. It is real (the KServe vLLM readiness probe flaps the endpoint out of the EndpointSlice under heavy load), but it was NOT the source of the load-beat 500s. That was flowControl admission shedding, now disabled on both sites (`vllm/serving.yaml` `featureGates: []`).
