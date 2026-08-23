#!/usr/bin/env bash
# Prove the gateway routes on live load, without putting an image in the cluster.
#
# kind here runs rootful, so loading a locally built gateway needs sudo. This
# runs the same binary on the host instead, against the real overlay and the
# real signals endpoint, with stand-in upstreams that name themselves.
#
# Both arms share one overlay file, so the only difference is the load block.
#
#   GW=/path/to/praxis-ai ./proof-routing.sh
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=grid-system
SITE="${SITE:-pool-a}"
SKEW="${SKEW:-pool-a}"
GW="${GW:?set GW to a praxis-ai binary}"
W="${W:-${HERE}/.generated/proof}"
mkdir -p "$W/routing"
ctx() { echo "kind-grid-llmd-pm-$1"; }
cleanup() { pkill -x praxis-ai 2>/dev/null || true; kill %1 %2 %3 %4 2>/dev/null || true; }
trap cleanup EXIT

kubectl --context "$(ctx "$SITE")" -n "$NS" get cm \
  grid-overlay-grid-llmd-pool-metrics-consumer-gateway \
  -o jsonpath='{.data.routing-overlay\.json}' > "$W/routing/routing-overlay.json"

kubectl --context "$(ctx "$SITE")" -n "$NS" port-forward svc/grid-operator-signals 19091:9091 >/dev/null 2>&1 &
sleep 3

# Stand-ins that name themselves, so a response says where the router sent it.
for p in 19001 19002 19003; do
  case $p in 19001) n=pool-a;; 19002) n=pool-b;; 19003) n=pool-c;; esac
  python3 -c "
import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length',0)))
        b=json.dumps({'served_by':'$n'}).encode()
        self.send_response(200); self.send_header('Content-Length',str(len(b))); self.end_headers()
        self.wfile.write(b)
    def log_message(self,*a): pass
http.server.HTTPServer(('127.0.0.1',$p),H).serve_forever()" >/dev/null 2>&1 &
done
sleep 2

cat > "$W/gw.yaml" <<EOF
listeners:
  - name: gw
    address: "127.0.0.1:18080"
    filter_chains: [route]
filter_chains:
  - name: route
    filters:
      - filter: json_body_field
        field: model
        header: X-Model
      - filter: intelligent_route
        overlay_file: ${W}/routing/routing-overlay.json
        model_header: X-Model
        expected_overlay_scope:
          network: grid-llmd-pool-metrics
          gateway: consumer-gateway
          namespace: ${NS}
          local_site: ${SITE}
        load:
          endpoint: "http://127.0.0.1:19091/metrics"
          queue_metric: "llm_d_epp_average_queue_size"
          collect:
            - "llm_d_epp_average_queue_size"
          interval_ms: 1000
          max_age_ms: 30000
      - filter: load_balancer
        clusters:
          - {name: llmd-pool-a-provider, endpoints: ["127.0.0.1:19001"]}
          - {name: llmd-pool-b-provider, endpoints: ["127.0.0.1:19002"]}
          - {name: llmd-pool-c-provider, endpoints: ["127.0.0.1:19003"]}
EOF
python3 - "$W" <<'PYEOF'
import sys
w = sys.argv[1]
s = open(f"{w}/gw.yaml").read()
i, j = s.index("        load:"), s.index("      - filter: load_balancer")
open(f"{w}/gw-noload.yaml", "w").write(s[:i] + s[j:])
PYEOF

arm() {
  pkill -x praxis-ai 2>/dev/null || true; sleep 1
  setsid "$GW" -c "$1" > "$W/$(basename "$1").log" 2>&1 < /dev/null &
  sleep 5
  for _ in $(seq 1 30); do
    curl -s -XPOST "http://127.0.0.1:18080/v1/chat/completions" -H 'Content-Type: application/json' \
      -d '{"model":"Qwen/Qwen3-0.6B","messages":[{"role":"user","content":"hi"}]}'
    echo
  done | sort | uniq -c | sed 's/^/    /'
}

echo "overlay order:"
python3 -c "
import json
d=json.load(open('$W/routing/routing-overlay.json'))
for c in d['overlay']['candidates']:
    print(f\"    rank={c.get('rank')} {c['site']:8} tier={c.get('selection_tier'):10} admission={c.get('admission_state')}\")"

echo "skewing ${SKEW}"
"${HERE}/load.sh" job "$SKEW" skew "http://vcr-service.${NS}.svc:8000" 40 300 0 >/dev/null 2>&1
sleep 60
curl -s "http://127.0.0.1:19091/metrics?collect[]=llm_d_epp_average_queue_size" \
  | sed 's/.*grid_site="/    /;s/",name.*} / queue=/;s/ [0-9]\{10,\}$//'

echo "arm A, overlay order only:"; arm "$W/gw-noload.yaml"
echo "arm B, plus live load:";     arm "$W/gw.yaml"
"${HERE}/load.sh" stop "$SKEW" skew >/dev/null 2>&1
