# Proxy-free path (file-discovery + ORIGINAL_DST) - inventory & unwind

Proven (HTTP 200, per-pool): the hub EPP gets endpoints from a ConfigMap and istio forwards its pick via an
ORIGINAL_DST EnvoyFilter - no proxy. But it trades **1 proxy pod/cluster** for the config below.

## What it costs (per N models × M clusters)

**Per hub EPP (per model):** 5 objects
- `ConfigMap` (file-discovery `config.yaml` + `endpoints.yaml`)
- `Deployment` (file-discovery mode, `--pool-name`, `--model-server-metrics-path=/metrics/model-X`)
- `Service` (ext_proc h2c)
- `InferencePool` marker (endpointPickerRef -> the EPP; selector matches nothing)
- **`EnvoyFilter`** REMOVE+ADD the pool's cluster as ORIGINAL_DST - **matched by the cluster's HASHED name**

**Hub (global):** `Gateway` + `HTTPRoute` (N demux rules on `x-model`)

**Per spoke (per cluster):** N `/metrics/<pool>` routes on the spoke gateway (URLRewrite -> each pool's EPP)

## The brittle bits

- **EnvoyFilter hash** - the cluster name (`clusters-ip-eb7e1d75`) embeds a hash of the InferencePool;
  **recreate the pool -> new hash -> the EnvoyFilter silently stops matching.** One EnvoyFilter patch-pair per pool.
- `EnvoyFilter` is "internal implementation details, may change on upgrade" (istio's own warning).
- Scales as **5 hub objects + per-pool spoke routes per model.**

-> The clean fix is upstream: an `InferencePool`/GIE option to forward ORIGINAL_DST (read the destination
header) instead of EDS+CLUSTER_PROVIDED, removing the EnvoyFilter entirely. Until then, **the proxy is the
simpler path** (1 pod/cluster, no hash, no EnvoyFilter).

## Unwind (remove the proxy-free prototype; the proxy path is untouched)

```bash
ctx=kind-aig-hub; ns=llm-d
kubectl --context $ctx -n $ns delete -f manifests/hub/file-discovery-original-dst.yaml
kubectl --context $ctx -n $ns delete -f manifests/hub/file-discovery-per-pool.yaml
kubectl --context $ctx -n $ns delete -f manifests/hub/file-discovery-gateway.yaml
kubectl --context $ctx -n $ns delete -f manifests/hub/file-discovery-epp.yaml
# spoke /metrics routes are harmless to leave; to revert, re-apply spoke-gateway.yaml without the /metrics rules
```

The proxy path (`hub-pool` + `cluster-proxies` + hub `Gateway` + `epp`) is on a separate gateway and remains
the working default.
