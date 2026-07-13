#!/usr/bin/env bash
# End-to-end proof of DELTA-RECOVERY.md approach B through the REAL agentic-api
# client: a multi-turn conversation, the Coordinator restarted mid-conversation
# (clearing its session cache), and the next turn STILL succeeds coherently
# because agentic-api transparently catches the 409 session_miss and retries with
# full history. The client never sees the miss.
set -u
NS=agentic-demo
MODEL="Qwen/Qwen2.5-1.5B-Instruct"
PORT=19000

pf() { pkill -f "port-forward.*${PORT}:9000" 2>/dev/null; sleep 1
  kubectl -n "$NS" port-forward svc/agentic-api ${PORT}:9000 >/tmp/pf-agentic.log 2>&1 & sleep 3; }

# turn <conv_id> <text> -> prints HTTP code + the assistant answer
turn() { local conv="$1" text="$2"
  curl -s -m 90 -o /tmp/aresp.json -w "%{http_code}" \
    "http://localhost:${PORT}/v1/responses" -H 'content-type: application/json' \
    -d "{\"model\":\"${MODEL}\",\"conversation_id\":\"${conv}\",\"store\":true,\"input\":$(printf '%s' "$text" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read()))')}"
}
answer() { python3 -c 'import json;d=json.load(open("/tmp/aresp.json"));print("".join(p.get("text","") for it in d.get("output",[]) for p in it.get("content",[])))' 2>/dev/null; }

pf
CONV=$(curl -s -m 20 "http://localhost:${PORT}/v1/conversations" -H 'content-type: application/json' -d '{}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])' 2>/dev/null)
echo "conversation: $CONV"

echo "== turn 1 (seq=0, seeds) =="; c=$(turn "$CONV" "My name is Sam and I am going to Tokyo."); echo "   HTTP $c :: $(answer | head -c 80)"
echo "== turn 2 (seq>0, delta) =="; c=$(turn "$CONV" "Also add Kyoto for two days."); echo "   HTTP $c :: $(answer | head -c 80)"

echo "== RESTART coordinator-real (clears session cache) =="
kubectl -n "$NS" rollout restart deploy/coordinator-real >/dev/null 2>&1
kubectl -n "$NS" rollout status deploy/coordinator-real --timeout=90s >/dev/null 2>&1
pf

echo "== turn 3 after restart (Coordinator 409s -> agentic-api retries full) =="
c=$(turn "$CONV" "What is my name and which cities am I visiting?")
ans=$(answer)
echo "   HTTP $c"
echo "   answer: $ans"
echo "$ans" | grep -qi sam && echo "$ans" | grep -qiE "tokyo|kyoto" \
  && echo "   PASS: transparent recovery, conversation intact" || echo "   FAIL: lost the conversation"

echo "== agentic-api log (retry fired?) =="
kubectl -n "$NS" logs -l app=agentic-api --since=60s 2>&1 | grep -i "session_miss\|retrying" | tail -3

pkill -f "port-forward.*${PORT}:9000" 2>/dev/null
