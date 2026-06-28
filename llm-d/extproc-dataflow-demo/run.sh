#!/usr/bin/env bash
# Real-body demo: IPP pre extracts the model from a real OpenAI body, EPP routes, IPP post injects
# auth (header-only) and translates the body ONLY for a different-API destination.
#   request A: model gpt-4    -> openai (OpenAI-compatible) -> auth only, body untouched
#   request B: model claude-3 -> anthropic (different API)  -> auth + body translated
set -uo pipefail
cd "$(dirname "$0")"

ENVOY_IMG="${ENVOY_IMG:-docker.io/envoyproxy/envoy:v1.31-latest}"
BIN=/tmp/extproc-demo
LOGDIR=/tmp/extproc-demo-logs
mkdir -p "$LOGDIR"

echo "== building =="
go build -o "$BIN" . || exit 1

fuser -k 18001/tcp 18002/tcp 18080/tcp >/dev/null 2>&1 || true   # clear any stale servers
sleep 0.3

echo "== starting mock servers (IPP and EPP are separate processes) =="
"$BIN" -role ipp  -port 18001 >"$LOGDIR/ipp.log"  2>&1 & P1=$!   # one IPP process, both hooks
"$BIN" -role epp  -port 18002 >"$LOGDIR/epp.log"  2>&1 & P2=$!
"$BIN" -role echo -port 18080 >"$LOGDIR/echo.log" 2>&1 & P3=$!
cleanup() { kill $P1 $P2 $P3 2>/dev/null || true; podman rm -f extproc-envoy >/dev/null 2>&1 || true; }
trap cleanup EXIT
sleep 1

echo "== starting envoy ($ENVOY_IMG) =="
podman rm -f extproc-envoy >/dev/null 2>&1 || true
podman run -d --rm --name extproc-envoy --network host --entrypoint envoy \
  -v "$PWD/envoy.yaml:/etc/envoy/envoy.yaml:Z" \
  "$ENVOY_IMG" -c /etc/envoy/envoy.yaml --log-level error >/dev/null
sleep 3

req() { # tenant model
  echo
  echo "== request: tenant=$1 model=$2 =="
  curl -s --max-time 12 -o /tmp/extproc-resp.json -w "HTTP %{http_code} (%{time_total}s)\n" \
    -X POST localhost:10000/v1/chat/completions \
    -H 'content-type: application/json' -H "x-tenant: $1" \
    -d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" || true
  echo "-- as the upstream (echo) saw it: auth header + (maybe translated) body --"
  jq '{provider: .headers["x-ipp-provider"], model: .headers["x-gateway-model-name"], authorization: .headers.authorization, body: (.body|fromjson)}' /tmp/extproc-resp.json 2>/dev/null || cat /tmp/extproc-resp.json
}

req acme   gpt-4      # OpenAI-compatible: auth only, no body buffer
req globex claude-3   # different API: auth + body translated

echo
echo "== ext_proc dataflow (mock logs) =="
for r in ipp epp; do echo "--- $r ---"; cat "$LOGDIR/$r.log" 2>/dev/null | grep -v listening; done
