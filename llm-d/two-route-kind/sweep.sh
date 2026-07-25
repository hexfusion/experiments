#!/usr/bin/env bash
# HTTP vs ext_proc against the same EPP across a concurrency ramp, with the CPU
# and memory each arm cost while it ran. Emits CSV to stdout.
#
# Decision latency is the TTFT contribution: the decider sits in front of the
# first token, so whatever it costs is added before generation starts.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER="${CLUSTER:-two-route}"
NS=two-route
REQUESTS="${REQUESTS:-20000}"
BODY_KB="${BODY_KB:-8}"
CONCS="${CONCS:-500 1000 2000 5000}"
BODIES="${BODIES:-}"   # set to sweep body size instead, at the first CONCS value

k() { kubectl --context "kind-$CLUSTER" "$@"; }
PROM="$(k -n $NS get svc prometheus -o jsonpath='{.spec.clusterIP}')"
EPP="$(k -n $NS get svc epp -o jsonpath='{.spec.clusterIP}')"

# Instant query against prometheus, printing a bare number.
q() {
  k -n istio-system exec deploy/istiod -- curl -sS -m 15 --data-urlencode "query=$1" \
    "http://$PROM:9090/api/v1/query" 2>/dev/null |
    python3 -c "
import json,sys
try:
    r=json.load(sys.stdin)['data']['result']
    print(f\"{sum(float(x['value'][1]) for x in r):.4f}\" if r else '0')
except Exception: print('0')
"
}
eppcpu() { q 'sum(rate(container_cpu_usage_seconds_total{namespace="two-route",pod=~"epp-.*",container!="",container!="POD"}[1m]))'; }
eppmem() { q 'sum(container_memory_working_set_bytes{namespace="two-route",pod=~"epp-.*",container!="",container!="POD"})'; }

# One arm at a time, selected by ARM, so an external sampler can attribute CPU
# and memory to a single transport rather than to both overlapping.
run() { # arm conc
  k -n $NS delete pod sweep --ignore-not-found >/dev/null 2>&1
  k -n $NS run sweep --restart=Never --image=localhost/two-route:dev --overrides="{\"spec\":{\"containers\":[{\"name\":\"sweep\",\"image\":\"localhost/two-route:dev\",\"imagePullPolicy\":\"IfNotPresent\",\"env\":[{\"name\":\"MODE\",\"value\":\"bench\"},{\"name\":\"ARM\",\"value\":\"$1\"},{\"name\":\"EPP_GRPC\",\"value\":\"$EPP:9002\"},{\"name\":\"EPP_HTTP\",\"value\":\"http://$EPP:9100\"},{\"name\":\"REQUESTS\",\"value\":\"$REQUESTS\"},{\"name\":\"CONCURRENCY\",\"value\":\"$2\"},{\"name\":\"BODY_KB\",\"value\":\"$BODY_KB\"}]}]}}" >/dev/null
  k -n $NS wait --for=jsonpath='{.status.phase}'=Succeeded pod/sweep --timeout=900s >/dev/null 2>&1
  k -n $NS logs sweep
}

echo "transport,concurrency,body_kb,p50_ms,p99_ms,rps,errors,epp_cpu_cores,epp_mem_mib"
if [ -n "$BODIES" ]; then
  PAIRS=""; C0="${CONCS%% *}"
  for b in $BODIES; do PAIRS="$PAIRS $C0:$b"; done
else
  PAIRS=""
  for c in $CONCS; do PAIRS="$PAIRS $c:$BODY_KB"; done
fi

for pair in $PAIRS; do
  c="${pair%%:*}"; BODY_KB="${pair##*:}"
  for arm in http ext_proc; do
    # Each arm runs alone, then CPU and memory are sampled while the 1m rate
    # window still covers only that arm.
    out="$(run "$arm" "$c")"
    cpu="$(eppcpu)"; mem="$(eppmem)"
    memmib="$(python3 -c "print(f'{float('$mem')/1048576:.0f}')")"
    line="$(grep "^$arm " <<<"$out" | head -1)"
    [ -n "$line" ] || continue
    python3 - "$arm" "$c" "$cpu" "$memmib" "$BODY_KB" <<PY
import re,sys
arm,conc,cpu,mem,bkb = sys.argv[1:6]
line = """$line"""
def ms(tok):
    m=re.match(r'([\d.]+)(ms|s|µs|us)$', tok)
    if not m: return 0.0
    v,u=float(m.group(1)),m.group(2)
    return v*1000 if u=='s' else v/1000 if u in ('µs','us') else v
t=line.split()
p50=ms(t[t.index('p50')+1]); p99=ms(t[t.index('p99')+1])
rps=float(t[t.index('req/s')-1]); errs=int(t[-1])
print(f"{arm},{conc},{bkb},{p50:.2f},{p99:.2f},{rps:.0f},{errs},{cpu},{mem}")
PY
    sleep 65
  done
done
