#!/usr/bin/env bash
# Front-door smoke: proves the serve path end to end after a payload roll.
# A request with no key is refused, a keyed request gets a completion that names the grid
# site and backend that served it, a batch of requests spreads over more than one site, and
# a streamed request emits SSE data lines. Exit 1 on any failure.
#
#   tests/frontdoor-smoke.sh            # defaults: maas.acme.lab via 192.168.1.201, 12 requests
#   N=24 MODEL=... tests/frontdoor-smoke.sh
#   GRID_CONTEXT=dagobah GRID_CONTEXTS="dagobah site-d" tests/frontdoor-smoke.sh
# Needs kubectl access to the hub (GRID_CONTEXT, else the current context) to read the
# gateway's decision counters, since the front door strips the grid's response headers.
# GRID_CONTEXTS lists every reachable cluster whose site identity should be checked.
set -u
HOST=${FRONT_DOOR_HOST:-maas.acme.lab}
IP=${ROUTER_IP:-192.168.1.201}
MODEL=${MODEL:-qwen3-coder-30b-a3b}
N=${N:-12}
KEY=$(tr -d '\r\n' < "${KEY_FILE:-$HOME/.config/grid-bench-maas-key}")
fail=0
ok() { echo "ok    $1"; }
bad() { echo "FAIL  $1"; fail=1; }
req() { curl -sk --max-time "${TIMEOUT:-90}" --resolve "$HOST:443:$IP" "$@"; }
auth=(-H "Authorization: Bearer $KEY" -H "content-type: application/json")
body='{"model":"'"$MODEL"'","messages":[{"role":"user","content":"Reply with the single word ok."}],"max_tokens":8}'

code=$(req -o /dev/null -w '%{http_code}' "https://$HOST/v1/models")
[ "$code" = 401 ] && ok "no key is refused with 401" || bad "no key answered $code, expected 401"

code=$(req -o /dev/null -w '%{http_code}' "${auth[@]}" "https://$HOST/v1/models")
[ "$code" = 200 ] && ok "key lists models" || bad "key on /v1/models answered $code"

# The front door does not pass the grid gateway's x-grid-site header back to the client, so
# site spread is read from the hub gateway's own routed-decision counters, summed over its
# replicas, before and after the batch.
decisions() {
  local pod port=19900 pf tmp
  tmp=$(mktemp)
  for pod in $(kubectl ${GRID_CONTEXT:+--context "$GRID_CONTEXT"} -n "${GRID_NAMESPACE:-grid}" get pods -l app.kubernetes.io/name=praxis-gateway -o name); do
    port=$((port + 1))
    kubectl ${GRID_CONTEXT:+--context "$GRID_CONTEXT"} -n "${GRID_NAMESPACE:-grid}" port-forward "$pod" "$port:9443" >/dev/null 2>&1 &
    pf=$!
    # The forward takes a moment to listen: retry rather than read nothing and call it zero.
    for _ in 1 2 3 4 5 6; do
      sleep 1
      curl -sk --max-time 5 "https://127.0.0.1:$port/metrics" > "$tmp" 2>/dev/null && [ -s "$tmp" ] && break
    done
    grep '^grid_route_decisions_total{' "$tmp" | grep 'reason="routed"'
    kill $pf 2>/dev/null
    wait $pf 2>/dev/null
  done | sed -E 's/.*site="([^"]+)".*cluster="([^"]+)".* ([0-9.e+]+)$/\1\/\2 \3/' | awk '{a[$1]+=$2} END {for (k in a) print k, a[k]}' | sort
  rm -f "$tmp"
}
before=$(decisions)
codes=()
for i in $(seq 1 "$N"); do
  # A nonce per request, so prefix affinity cannot pin the whole batch to one site and the
  # spread below measures site selection.
  codes+=("$(req -o /dev/null -w '%{http_code}' "${auth[@]}" -d "${body/Reply with/Request $i: reply with}" "https://$HOST/v1/chat/completions")")
done
after=$(decisions)
non200=$(printf '%s\n' "${codes[@]}" | grep -vc '^200$')
[ "$non200" = 0 ] && ok "$N completions all 200" || bad "$non200 of $N completions not 200: ${codes[*]}"
spread=$(join <(echo "$before") <(echo "$after") | awk '$3 > $2 {print $1, $3 - $2}')
routed=$(echo "$spread" | awk '{s+=$2} END {print s+0}')
sites=$(echo "$spread" | grep -c .)
[ "$routed" -ge "$N" ] && ok "the hub gateway routed $routed decisions during the batch" || bad "the hub gateway counted $routed routed decisions for $N requests"
[ "$sites" -ge 2 ] && ok "spread over $sites site clusters" || bad "all routed decisions went to $sites cluster"
echo "$spread" | sed 's/^/      /'

stream='{"model":"'"$MODEL"'","messages":[{"role":"user","content":"Count to five."}],"max_tokens":16,"stream":true}'
lines=$(req "${auth[@]}" -d "$stream" "https://$HOST/v1/chat/completions" | grep -c '^data:')
[ "$lines" -ge 2 ] && ok "streaming emits $lines SSE data lines" || bad "streaming emitted $lines data lines"

# The grid behind the door. The front door kept answering through the hub for a whole day
# while every site identity was expired and the peers sat in Connecting, so a pass above is
# not a healthy grid. Identity lifetime is 4 h in the lab: fail when under 30 min remain.
kc() { kubectl ${GRID_CONTEXT:+--context "$GRID_CONTEXT"} -n "${GRID_NAMESPACE:-grid}" "$@"; }
now=$(date +%s)
for ctx in ${GRID_CONTEXTS:-${GRID_CONTEXT:-}}; do
  end=$(kubectl --context "$ctx" -n "${GRID_NAMESPACE:-grid}" get secret grid-site-identity -o jsonpath='{.data.tls\.crt}' 2>/dev/null | base64 -d | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
  if [ -z "$end" ]; then bad "$ctx: no site identity readable"; continue; fi
  left=$(( $(date -d "$end" +%s) - now ))
  [ "$left" -gt 1800 ] && ok "$ctx identity valid for $((left / 60)) min" || bad "$ctx identity expires in $((left / 60)) min ($end)"
done
phases=$(kc get gridsite -o jsonpath='{range .items[*]}{.metadata.name}={.status.phase}{"\n"}{end}' 2>/dev/null)
stuck=$(echo "$phases" | grep -vE '=(Active|Discovered)$' | grep -c . )
[ "$stuck" = 0 ] && ok "every GridSite is Active or Discovered" || bad "GridSites not Active: $(echo "$phases" | grep -vE '=(Active|Discovered)$' | tr '\n' ' ')"
peers=$(echo "$phases" | grep -c '=Active$')
[ "$peers" -ge 1 ] && ok "$peers peer sites Active" || bad "no peer site is Active, the mesh is down"

[ "$fail" = 0 ] && echo "grid: PASS" || echo "grid: FAIL"
exit $fail
