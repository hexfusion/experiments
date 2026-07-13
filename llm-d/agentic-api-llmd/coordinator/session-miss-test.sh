#!/usr/bin/env bash
# Proves DELTA-RECOVERY.md approach B on the real Coordinator:
#   seed (seq=0) -> delta (seq=1) -> restart Coordinator (clears the in-memory
#   session cache) -> delta (seq=2) MUST 409 session_miss -> retry full history
#   (seq=0) MUST 200 and answer coherently. Delta stays an optimization over a
#   correct floor.
set -u
NS=agentic-demo
MODEL="Qwen/Qwen2.5-1.5B-Instruct"
SID="miss-test-$$"
PF_PORT=18080

pf() { pkill -f "port-forward.*${PF_PORT}:8080" 2>/dev/null; sleep 1
  kubectl -n "$NS" port-forward svc/coordinator-real ${PF_PORT}:8080 >/tmp/pf-coord.log 2>&1 & sleep 3; }

# send <seq> <text> -> prints "HTTP <code>" and a trimmed body
send() { local seq="$1" text="$2"
  curl -s -m 60 -o /tmp/resp.json -w "%{http_code}" \
    "http://localhost:${PF_PORT}/v1/responses" \
    -H 'content-type: application/json' \
    -H "x-session-id: ${SID}" \
    -H "x-session-seq: ${seq}" \
    -d "{\"model\":\"${MODEL}\",\"input\":$(printf '%s' "$text" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read()))'),\"cache_salt\":\"miss-demo\"}"; }

pf
echo "== 1. seed (seq=0)  =>  expect 200"
c=$(send 0 "My name is Sam and I am going to Tokyo."); echo "   HTTP $c"
echo "== 2. delta (seq=1) =>  expect 200"
c=$(send 1 "Also add Kyoto for two days."); echo "   HTTP $c"

echo "== 3. RESTART coordinator-real (clears in-memory session cache) =="
kubectl -n "$NS" rollout restart deploy/coordinator-real >/dev/null 2>&1
kubectl -n "$NS" rollout status deploy/coordinator-real --timeout=90s >/dev/null 2>&1
pf

echo "== 4. delta (seq=2) after restart =>  expect 409 session_miss"
c=$(send 2 "What is my name and which cities am I visiting?")
echo "   HTTP $c  body: $(head -c 80 /tmp/resp.json)"
[ "$c" = "409" ] && echo "   PASS: miss detected" || echo "   FAIL: expected 409"

echo "== 5. retry full history (seq=0) =>  expect 200 + coherent"
c=$(send 0 "My name is Sam and I am going to Tokyo. Also add Kyoto for two days. What is my name and which cities am I visiting?")
echo "   HTTP $c"
ans=$(python3 -c 'import json;d=json.load(open("/tmp/resp.json"));print("".join(p.get("text","") for it in d.get("output",[]) for p in it.get("content",[])))' 2>/dev/null)
echo "   answer: $ans"
echo "$ans" | grep -qi "sam" && echo "$ans" | grep -qiE "tokyo|kyoto" \
  && echo "   PASS: recovered coherently" || echo "   (check coherence manually)"

pkill -f "port-forward.*${PF_PORT}:8080" 2>/dev/null
