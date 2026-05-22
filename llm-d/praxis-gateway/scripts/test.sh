#!/bin/bash
set -euo pipefail

GW="${GW:-localhost:8080}"
N="${N:-5}"
PROMPT="${PROMPT:-The capital of France is Paris and the capital of Spain is Madrid and the capital of Italy is}"

cd "$(dirname "${BASH_SOURCE[0]}")/.."

hit() {
  local model="$1"; shift
  local label="$1"; shift
  local body
  body=$(jq -nc --arg m "$model" --arg p "$PROMPT" \
    '{model:$m, prompt:$p, max_tokens:5}')
  local status
  status=$(curl -sS -o /dev/null -w '%{http_code}' "${GW}/v1/completions" \
    -H "Content-Type: application/json" \
    -H "X-Tenant-Id: demo" \
    -H "X-Request-Id: $(uuidgen)" \
    -d "$body")
  echo "  $label model=$model status=$status"
}

echo "== ${N} requests with model=qwen-7b-awq (consistent-hash should pin to one pod) =="
for i in $(seq 1 "$N"); do
  hit "qwen-7b-awq" "req $i"
done

echo
echo "== ${N} requests with model=mistral-7b-instruct (different hash, likely different pod) =="
for i in $(seq 1 "$N"); do
  hit "mistral-7b-instruct" "req $i"
done

echo
echo "== Per-pod request counts (post-run) =="
for pod in vllm-sim-1 vllm-sim-2 vllm-sim-3; do
  count=$(podman compose logs "$pod" 2>/dev/null | grep -c 'POST /v1/completions' || true)
  echo "  $pod  count=${count:-0}"
done

echo
echo "== Done. =="
echo "  Praxis access log:  podman compose logs praxis"
echo "  Jaeger UI:          http://localhost:16686"
