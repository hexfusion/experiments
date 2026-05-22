#!/bin/bash
set -euo pipefail

NS="${NS:-llm-d-praxis}"
N="${N:-5}"
MODEL="${MODEL:-qwen-7b-awq}"
PROMPT="${PROMPT:-The capital of France is Paris and the capital of Spain is Madrid and the capital of Italy is}"

PF_PID=""
trap '[[ -n "$PF_PID" ]] && kill "$PF_PID" 2>/dev/null || true' EXIT

if HOST=$(kubectl -n "$NS" get route praxis -o jsonpath='{.spec.host}' 2>/dev/null) && [[ -n "$HOST" ]]; then
  GW="https://${HOST}"
  CURL_OPTS=(-k)
  echo "==> using Route: ${GW}"
else
  echo "==> port-forwarding praxis to localhost:8080"
  kubectl -n "$NS" port-forward svc/praxis 8080:8080 >/dev/null 2>&1 &
  PF_PID=$!
  sleep 2
  GW="http://localhost:8080"
  CURL_OPTS=()
fi

hit() {
  local i="$1"; shift
  local body
  body=$(jq -nc --arg m "$MODEL" --arg p "$PROMPT" \
    '{model:$m, prompt:$p, max_tokens:5}')
  local start end ms status
  start=$(date +%s%3N)
  status=$(curl -sS "${CURL_OPTS[@]}" -o /dev/null -w '%{http_code}' "${GW}/v1/completions" \
    -H "Content-Type: application/json" \
    -H "X-Request-Id: $(uuidgen)" \
    -d "$body")
  end=$(date +%s%3N)
  ms=$((end - start))
  echo "  req $i status=$status latency=${ms}ms"
}

echo
echo "== ${N} completion requests through praxis -> vllm =="
for i in $(seq 1 "$N"); do
  hit "$i"
done

echo
echo "== praxis access log (tail) =="
kubectl -n "$NS" logs --tail=10 deployment/praxis 2>/dev/null || true

echo
echo "== vllm log (tail) =="
kubectl -n "$NS" logs --tail=10 deployment/vllm 2>/dev/null || true
