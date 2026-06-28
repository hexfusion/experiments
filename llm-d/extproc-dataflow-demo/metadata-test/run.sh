#!/usr/bin/env bash
# Settle: does a pre-EPP filter's request-BODY phase receive metadata the EPP set in its HEADER phase?
set -uo pipefail
cd "$(dirname "$0")"

ENVOY_IMG="${ENVOY_IMG:-docker.io/envoyproxy/envoy:v1.31-latest}"
BIN=/tmp/md-test
LOG=/tmp/md-test-logs
mkdir -p "$LOG"

echo "== build =="
go build -o "$BIN" . || exit 1

echo "== start servers =="
"$BIN" -role ipp   -port 19001 >"$LOG/ipp.log"   2>&1 & P1=$!
"$BIN" -role epp   -port 19002 >"$LOG/epp.log"   2>&1 & P2=$!
"$BIN" -role probe -port 19003 >"$LOG/probe.log" 2>&1 & P3=$!
"$BIN" -role echo  -port 18090 >"$LOG/echo.log"  2>&1 & P4=$!
cleanup() { kill $P1 $P2 $P3 $P4 2>/dev/null || true; podman rm -f md-envoy >/dev/null 2>&1 || true; }
trap cleanup EXIT
sleep 1

echo "== start envoy =="
podman rm -f md-envoy >/dev/null 2>&1 || true
podman run -d --rm --name md-envoy --network host --entrypoint envoy \
  -v "$PWD/envoy.yaml:/etc/envoy/envoy.yaml:Z" "$ENVOY_IMG" \
  -c /etc/envoy/envoy.yaml --log-level error >/dev/null
sleep 3

echo "== one request (with a body so the IPP body phase fires) =="
curl -s --max-time 10 -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST localhost:10001/v1/chat/completions \
  -H 'content-type: application/json' -d '{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}' || true

echo
echo "== RESULT =="
echo "--- epp (what it set) ---";        cat "$LOG/epp.log"   2>/dev/null | grep -v listening
echo "--- probe/hdr (CONTROL, post-EPP) ---"; cat "$LOG/probe.log" 2>/dev/null | grep -v listening
echo "--- ipp (pre-EPP header, then body = THE ANSWER) ---"; cat "$LOG/ipp.log" 2>/dev/null | grep -v listening
echo
echo "== verdict =="
PROBE=$(grep -c 'envoy.lb.pick="azure' "$LOG/probe.log" 2>/dev/null); PROBE=${PROBE:-0}
IPPBODY=$(grep 'ipp/body' "$LOG/ipp.log" 2>/dev/null | grep -c 'envoy.lb.pick="azure'); IPPBODY=${IPPBODY:-0}
IPPFIRED=$(grep -c 'ipp/body' "$LOG/ipp.log" 2>/dev/null); IPPFIRED=${IPPFIRED:-0}
echo "probe saw the metadata:        $([ "$PROBE" -gt 0 ] && echo YES || echo NO)"
echo "ipp body phase fired at all:   $([ "$IPPFIRED" -gt 0 ] && echo YES || echo NO)"
echo "ipp BODY phase saw metadata:   $([ "$IPPBODY" -gt 0 ] && echo YES || echo NO)"
if [ "$PROBE" -gt 0 ] && [ "$IPPFIRED" -gt 0 ] && [ "$IPPBODY" -eq 0 ]; then
  echo ">> NOT a config issue: metadata is set+forwardable (probe got it), the body message fired,"
  echo ">> but a pre-EPP filter's body phase does NOT carry the EPP's later-set metadata."
elif [ "$IPPBODY" -gt 0 ]; then
  echo ">> Metadata IS available in the pre-EPP body phase -> a single IPP filter CAN read the pick there."
elif [ "$PROBE" -eq 0 ]; then
  echo ">> CONFIG issue: even the post-EPP probe didn't get it (EPP metadata not saved/forwarded)."
fi
