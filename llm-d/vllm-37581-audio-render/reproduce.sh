#!/bin/bash
# Reproduce vllm#37581 — audio render JSON-serialization crash
#
# Tracking: RHOAIENG-63587 / https://github.com/vllm-project/vllm/issues/37581
#
# Expected outcome: TypeError: Object of type MultiModalKwargsItems is not JSON serializable
# in the vLLM server stderr.

set -euo pipefail

VLLM_DIR="${VLLM_DIR:-$HOME/projects/vllm-project/vllm}"
MODEL="${MODEL:-Qwen/Qwen3-ASR-0.6B}"
PORT="${PORT:-8000}"
PAYLOAD="${PAYLOAD:-$(dirname "$0")/audio-payload.json}"
LOG_FILE="${LOG_FILE:-$(dirname "$0")/stacktrace.log}"

cd "$VLLM_DIR"

# Pin SHA for reproducibility
PINNED_SHA=$(git rev-parse HEAD)
echo "vLLM SHA: $PINNED_SHA" | tee "$LOG_FILE"
echo "Model: $MODEL" | tee -a "$LOG_FILE"
echo "---" | tee -a "$LOG_FILE"

# Start vLLM
echo "Starting vLLM..."
vllm serve "$MODEL" \
  --port "$PORT" \
  --limit-mm-per-prompt audio=4 \
  2>&1 | tee -a "$LOG_FILE" &
VLLM_PID=$!

# Wait for readiness
echo "Waiting for /health..."
until curl -sf "http://localhost:$PORT/health" > /dev/null 2>&1; do
  if ! kill -0 "$VLLM_PID" 2>/dev/null; then
    echo "vLLM died before becoming ready. See $LOG_FILE"
    exit 1
  fi
  sleep 2
done
echo "vLLM ready."

# Trigger the bug
echo "Sending input_audio chat-completions/render request..."
curl -X POST "http://localhost:$PORT/v1/chat/completions/render" \
  -H 'Content-Type: application/json' \
  -d @"$PAYLOAD" \
  | tee -a "$LOG_FILE"

# Give vLLM a moment to emit the traceback
sleep 2

# Capture state
echo "---" | tee -a "$LOG_FILE"
echo "Done. Inspect $LOG_FILE for the TypeError." | tee -a "$LOG_FILE"

# Cleanup
kill "$VLLM_PID" 2>/dev/null || true
wait "$VLLM_PID" 2>/dev/null || true
