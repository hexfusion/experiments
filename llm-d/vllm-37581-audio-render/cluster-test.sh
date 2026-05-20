#!/bin/bash
# End-to-end cluster reproducer for vllm#37581.
#
# Prereqs:
#   - Audio fixture generated: ./make-sample.sh
#   - HF token at ~/.config/hf-token (script creates the namespace secret from it)
#   - kubectl context set to dagobah cluster
#
# What it does:
#   1. Apply manifests
#   2. Wait for Pod ready (model download can take a while; readinessProbe gives 10 min)
#   3. Port-forward to localhost:8000
#   4. Send the input_audio chat-completions/render request
#   5. Capture response + Pod stderr
#   6. Tear down (port-forward only; namespace persists for log inspection)

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
NS="vllm-37581"
LOG="$DIR/cluster-stacktrace.log"
PAYLOAD="$DIR/audio-payload.json"

[ -f "$PAYLOAD" ] || { echo "Run ./make-sample.sh first to generate $PAYLOAD"; exit 1; }
grep -q REPLACE_WITH_BASE64_AUDIO "$PAYLOAD" && { echo "$PAYLOAD has a placeholder — run ./make-sample.sh"; exit 1; }

echo "== Ensuring hf-token secret in $NS =="
"$DIR/../scripts/ensure-hf-token.sh" "$NS" | tee "$LOG"

echo "== Applying manifests =="
kubectl apply -f "$DIR/manifests/" | tee -a "$LOG"

echo "== Waiting for Pod ready (up to 10 min for model download) =="
kubectl -n "$NS" wait --for=condition=ready pod -l app=vllm-qwen3-asr --timeout=600s | tee -a "$LOG"

echo "== Starting port-forward =="
kubectl -n "$NS" port-forward svc/vllm-qwen3-asr 8000:8000 > /dev/null &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT
sleep 3

echo "== Sending input_audio render request =="
echo "--- response body ---" | tee -a "$LOG"
curl -sS -X POST http://localhost:8000/v1/chat/completions/render \
  -H 'Content-Type: application/json' \
  -d @"$PAYLOAD" \
  | tee -a "$LOG"
echo "" | tee -a "$LOG"

echo "== Capturing Pod stderr (last 100 lines) =="
echo "--- pod stderr ---" | tee -a "$LOG"
kubectl -n "$NS" logs -l app=vllm-qwen3-asr --tail=100 2>&1 | tee -a "$LOG"

echo ""
echo "== Collecting full artifact snapshot =="
"$DIR/collect-artifacts.sh"

echo ""
echo "Done. Inline log: $LOG. Full snapshot under: $DIR/artifacts/"
echo "Expected: 'TypeError: Object of type MultiModalKwargsItems is not JSON serializable'"
