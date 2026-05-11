#!/bin/bash
# Same warm-up + same-prefix + unique-prefix scenarios as 02-vllm-sim/test.sh,
# pointed at the kind-port-mapped Gateway (host :8080 → node :30080).
set -euo pipefail

GW="${GW:-localhost:18080}"
PROMPT="The capital of France is Paris and the capital of Spain is Madrid and the capital of Italy is"
MODEL="TinyLlama/TinyLlama-1.1B-Chat-v1.0"

hit() {
  local label="$1"; shift
  local body="$1"; shift
  local served
  served=$(curl -sS -D - -o /dev/null "${GW}/v1/completions" \
    -H "Content-Type: application/json" \
    -H "X-Tenant-Id: demo" \
    -H "X-Request-Id: $(uuidgen)" \
    -d "$body" \
    | awk 'tolower($1)=="x-inference-pod:"{print $2; exit}' | tr -d '\r')
  echo "  $label served_by=${served:-?}"
}

echo "== Warm-up =="
hit "warmup" "$(jq -nc --arg p "$PROMPT" --arg m "$MODEL" '{model:$m,prompt:$p,max_tokens:20}')"

echo
echo "== Wait 2s for KVEvents to flow =="
sleep 2

echo
echo "== Five same-prefix requests (should converge to one pod) =="
for i in $(seq 1 5); do
  hit "req $i" "$(jq -nc --arg p "$PROMPT" --arg m "$MODEL" '{model:$m,prompt:$p,max_tokens:5}')"
done

echo
echo "== Five unique-prefix requests (round-robin fallback) =="
for i in $(seq 1 5); do
  hit "req $i" "$(jq -nc --arg p "unrelated prompt $i $(uuidgen)" --arg m "$MODEL" '{model:$m,prompt:$p,max_tokens:5}')"
done

echo
echo "== Done. =="
echo "  EPP logs:    kubectl logs -n llm-d -l app=epp -f"
echo "  Sim logs:    kubectl logs -n llm-d -l app=vllm-sim -f"
echo "  Jaeger UI:   http://localhost:16686"
