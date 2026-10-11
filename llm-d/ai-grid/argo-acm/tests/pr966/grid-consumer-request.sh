#!/usr/bin/env bash
# One chat completion through the grid-consumer gateway (ClusterIP, auth none) via port-forward.
set -u
OUT="${1:-/tmp/grid-consumer-response.json}"
kubectl port-forward -n grid svc/grid-consumer 18080:8080 >/dev/null 2>&1 &
PF=$!
sleep 3
curl -s -m 90 -o "$OUT" -w 'http=%{http_code} time=%{time_total}s\n' \
  -H 'content-type: application/json' \
  -d '{"model":"Qwen3-Coder-30B-A3B","messages":[{"role":"user","content":"Reply with the single word pong."}],"max_tokens":8}' \
  http://127.0.0.1:18080/v1/chat/completions
kill $PF 2>/dev/null
jq -c '{model, choice: .choices[0].message.content, usage}' "$OUT" 2>/dev/null || head -c 400 "$OUT"
