#!/usr/bin/env bash
# Arm 2 — TTFT vs prompt size, low concurrency. Isolates the ~17ms JSON cost from
# the ext_proc body-stream + queue tail. Pair with EPP Prometheus for route time.
set -euo pipefail
BASE_URL="${BASE_URL:?set BASE_URL}"
MODEL="${MODEL:-qwen-7b-awq}"
RATE="${RATE:-8}"                      # low, fixed concurrency
SIZES="${SIZES:-256 4096 56000 131072}" # prompt tokens ≈ 1KB,16KB,220KB,512KB
for pt in $SIZES; do
  guidellm benchmark \
    --target "$BASE_URL" --model "$MODEL" \
    --rate-type concurrent --rate "$RATE" \
    --data "prompt_tokens=$pt,output_tokens=128" \
    --max-seconds 60 \
    --output-path "results/guidellm-pt$pt.json"
done
