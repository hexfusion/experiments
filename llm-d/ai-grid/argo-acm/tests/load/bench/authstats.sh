#!/usr/bin/env bash
# Snapshot MaaS auth-path counters: the wasm-shim's allowed/denied/errors on maas-default-gateway,
# and Authorino's gRPC Check histogram summed over every authorino pod. authstats.sh OUT.json
K="kubectl --kubeconfig ${GRID_KUBECONFIG:-$HOME/.kube/grid.kubeconfig} --context dagobah"
P=$($K -n openshift-ingress get pods -o name | grep maas-default | head -1)
{
  $K -n openshift-ingress exec $P -- pilot-agent request GET stats 2>/dev/null | grep -E '^wasmcustom\.kuadrant\.'
  for a in $($K -n kuadrant-system get pods -o name | grep -E 'pod/authorino-[0-9a-f]+-'); do
    $K -n kuadrant-system exec $a -- curl -s localhost:8080/server-metrics 2>/dev/null \
      | grep -E '^grpc_server_handling_seconds_(bucket|count|sum)\{.*grpc_method="Check"'
  done
} | python3 -c '
import sys,json,re,collections
o={"wasm":{},"check":collections.defaultdict(float)}
for l in sys.stdin:
    l=l.strip()
    if l.startswith("wasmcustom"):
        k,v=l.split(": "); o["wasm"][k.split(".")[-1]]=float(v)
    else:
        name,val=l.rsplit(" ",1); le=re.search(r"le=\"([^\"]+)\"",name)
        key=("bucket:"+le.group(1)) if le else name.split("{")[0].rsplit("_",1)[-1]
        o["check"][key]+=float(val)
print(json.dumps(o))' > "$1"
