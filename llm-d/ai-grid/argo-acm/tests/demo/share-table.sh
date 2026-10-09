#!/bin/bash
# usage: share-table.sh <start HH:MM:SS UTC> <end HH:MM:SS UTC>
KC=~/.kube/grid.kubeconfig; TOKEN=$(oc --kubeconfig $KC --context dagobah whoami -t); Q=https://thanos-querier-openshift-monitoring.apps.dagobah.hexfusion.local
S=$(date -u -d "$1" +%s); E=$(date -u -d "$2" +%s); D=${TMPDIR:-/tmp}
curl -sk -H "Authorization: Bearer $TOKEN" "$Q/api/v1/query_range" --data-urlencode 'query=sum by (backend) (rate(grid_route_decisions_total{reason="routed"}[30s])) / ignoring(backend) group_left sum(rate(grid_route_decisions_total{reason="routed"}[30s]))' --data-urlencode "start=$S" --data-urlencode "end=$E" --data-urlencode "step=10" > $D/share.json
curl -sk -H "Authorization: Bearer $TOKEN" "$Q/api/v1/query_range" --data-urlencode 'query=sum(grid_provider_in_flight_requests{grid_provider="factory"})' --data-urlencode "start=$S" --data-urlencode "end=$E" --data-urlencode "step=10" > $D/inflight.json
python3 -I - <<'PY'
import json,datetime
D=os.environ.get("TMPDIR","/tmp")+"/"
sh=json.load(open(D+'share.json'))['data']['result']; inf=json.load(open(D+'inflight.json'))['data']['result']
series={s['metric']['backend']:dict(s['values']) for s in sh}; infl=dict(inf[0]['values']) if inf else {}
ts=sorted({t for s in series.values() for t in s})
print(f"{'time':9s} {'east%':>5s} {'west%':>5s} {'portl%':>6s} {'P inflight':>10s}")
for t in ts:
    f=lambda b: float(series.get(b,{}).get(t,'nan'))*100
    print(f"{datetime.datetime.fromtimestamp(t,datetime.UTC).strftime('%H:%M:%S'):9s} {f('hq-east'):5.0f} {f('hq-west'):5.0f} {f('factory'):6.0f} {float(infl.get(t,'nan')):10.0f}")
PY
