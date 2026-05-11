#!/bin/bash
# Assumes `podman compose up -d` has been run from this directory.
#
# Demo of prefix-aware routing:
#   1. First request with prompt P1 → EPP picks via round-robin fallback
#      (no kvcache hits yet). Sim processes P1, publishes KVEvents over
#      ZMQ; epp-mock's indexer ingests them within ~1s.
#   2. Second request with the same prompt prefix → EPP scores pods,
#      finds the pod that has P1's blocks cached, picks it deterministically.
#
# served_by below is read from the x-served-by response header (set by
# Envoy via %UPSTREAM_HOST%) — that's the actual pod IP:port the request
# landed on, regardless of what the response body looks like.
#
# Note on x-request-id: Envoy uses character 14 of the request_id UUID to
# decide tracing status. Non-UUID request_ids are classified `not_traceable`.
set -euo pipefail

GW="${GW:-localhost:8080}"
PROMPT="The capital of France is Paris and the capital of Spain is Madrid and the capital of Italy is"

# Issue one completion request and print the served pod.
hit() {
  local label="$1"; shift
  local body="$1"; shift
  local served
  served=$(curl -sS -D - -o /dev/null "${GW}/v1/completions" \
    -H "Content-Type: application/json" \
    -H "X-Tenant-Id: demo" \
    -H "X-Request-Id: $(uuidgen)" \
    -d "$body" \
    | awk -F'[:[:space:]]+' 'tolower($1)=="x-served-by"{print $2; exit}')
  echo "  $label served_by=${served:-?}"
}

echo "== Warm-up: send prompt once to seed kv-cache on whichever pod gets picked =="
hit "warmup" "$(jq -nc --arg p "$PROMPT" '{model:"qwen-7b-awq",prompt:$p,max_tokens:20}')"

echo
echo "== Wait 2s for KVEvents to flow through ZMQ → indexer =="
sleep 2

echo
echo "== Five requests with the same prefix (should all converge to one pod) =="
for i in $(seq 1 5); do
  hit "req $i" "$(jq -nc --arg p "$PROMPT" '{model:"qwen-7b-awq",prompt:$p,max_tokens:5}')"
done

echo
echo "== Five requests with DIFFERENT prefixes (should round-robin: no prefix match) =="
for i in $(seq 1 5); do
  hit "req $i" "$(jq -nc --arg p "unrelated prompt $i $(uuidgen)" '{model:"qwen-7b-awq",prompt:$p,max_tokens:5}')"
done

echo
echo "== Done. =="
echo "  Logs:    podman compose logs -f epp-mock"
echo "  Traces:  http://localhost:16686 (Service: envoy → look for llm_d.kv_cache.score_tokens spans)"
