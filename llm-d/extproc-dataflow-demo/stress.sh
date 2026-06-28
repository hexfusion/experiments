#!/usr/bin/env bash
# Adversarial stress: each request carries a distinct tenant + model, on a real OpenAI body. We
# assert every response's injected auth, provider, and translation match THAT request. Under the
# concurrent burst, any cross-request mixup (x-request-id correlation) mismatches and exits non-zero.
#   gpt-4    -> openai    : auth sk-openai/<tenant>:gold, body NOT translated
#   claude-3 -> anthropic : auth sk-ant/<tenant>:gold,    body translated
set -uo pipefail
cd "$(dirname "$0")"

ENVOY_IMG="${ENVOY_IMG:-docker.io/envoyproxy/envoy:v1.31-latest}"
BIN=/tmp/extproc-demo
LOGDIR=/tmp/extproc-demo-logs
TMP=/tmp/extproc-stress
mkdir -p "$LOGDIR" "$TMP"; rm -f "$TMP"/*.json
command -v jq >/dev/null || { echo "need jq"; exit 1; }

echo "== building =="; go build -o "$BIN" . || exit 1
fuser -k 18001/tcp 18002/tcp 18080/tcp >/dev/null 2>&1 || true; sleep 0.3

echo "== starting servers =="
"$BIN" -role ipp  -port 18001 >"$LOGDIR/ipp.log"  2>&1 & P1=$!
"$BIN" -role epp  -port 18002 >"$LOGDIR/epp.log"  2>&1 & P2=$!
"$BIN" -role echo -port 18080 >"$LOGDIR/echo.log" 2>&1 & P3=$!
cleanup() { kill $P1 $P2 $P3 2>/dev/null || true; podman rm -f extproc-envoy >/dev/null 2>&1 || true; }
trap cleanup EXIT; sleep 1

echo "== starting envoy =="
podman rm -f extproc-envoy >/dev/null 2>&1 || true
podman run -d --rm --name extproc-envoy --network host --entrypoint envoy \
  -v "$PWD/envoy.yaml:/etc/envoy/envoy.yaml:Z" "$ENVOY_IMG" -c /etc/envoy/envoy.yaml --log-level error >/dev/null
sleep 3

TENANTS=(acme globex initech wayne umbrella)
MODELS=(gpt-4 claude-3)

PASS=0; FAIL=0
check() { # file tenant model
  local f="$1" t="$2" m="$3" wprov wauth wxlated gprov gauth gxlated
  if [ "$m" = claude-3 ]; then wprov=anthropic; wauth="Bearer sk-ant/$t:gold"; wxlated=true
  else wprov=openai; wauth="Bearer sk-openai/$t:gold"; wxlated=false; fi
  gprov=$(jq -r '.headers["x-ipp-provider"]//"X"' "$f" 2>/dev/null)
  gauth=$(jq -r '.headers.authorization//"X"' "$f" 2>/dev/null)
  gxlated=$(jq -r '(.body|fromjson|has("_translated_from"))//false' "$f" 2>/dev/null)
  if [ "$gprov" = "$wprov" ] && [ "$gauth" = "$wauth" ] && [ "$gxlated" = "$wxlated" ]; then PASS=$((PASS+1))
  else FAIL=$((FAIL+1)); echo "  FAIL t=$t m=$m want[$wprov|$wauth|xlate=$wxlated] got[$gprov|$gauth|xlate=$gxlated]"; fi
}
fire() { # file tenant model
  curl -s --max-time 15 -o "$1" -X POST localhost:10000/v1/chat/completions \
    -H 'content-type: application/json' -H "x-tenant: $2" \
    -d "{\"model\":\"$3\",\"messages\":[{\"role\":\"user\",\"content\":\"hi from $2\"}]}"
}

echo; echo "== phase 1: sequential, every tenant x model =="
i=0; for t in "${TENANTS[@]}"; do for m in "${MODELS[@]}"; do
  f="$TMP/seq.$i.json"; fire "$f" "$t" "$m"; check "$f" "$t" "$m"; i=$((i+1)); done; done
echo "  sequential: $PASS passed, $FAIL failed"

echo; echo "== phase 2: concurrent burst (60, distinct tenant/model each) =="
N=60; declare -a CT CM
for ((i=0;i<N;i++)); do t="${TENANTS[$((i%5))]}"; m="${MODELS[$((i%2))]}"; CT[$i]=$t; CM[$i]=$m
  fire "$TMP/conc.$i.json" "$t" "$m" & done
wait
for ((i=0;i<N;i++)); do check "$TMP/conc.$i.json" "${CT[$i]}" "${CM[$i]}"; done

echo; echo "== result: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ] && echo "  ALL GOOD -- per-request auth + translation held, including the concurrent burst" || { echo "  STRESS FAILED"; exit 1; }
