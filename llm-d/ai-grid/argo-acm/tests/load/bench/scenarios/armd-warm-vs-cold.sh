#!/usr/bin/env bash
cd "$(dirname "$0")/.."
EV=~/.local/share/grid-bench/sD-events.txt; : > $EV
# Warm hq-west only, straight at its vLLM Service, with the shared ~7500-token system prompt.
echo "warm_start $(date -u +%FT%TZ)" >> $EV
LABEL=sD-warm-hq-west PREFIX_TOKENS=7500 COUNT=20 RATES=1 STEP_SECONDS=20 MAX_CONCURRENCY=8 OUTPUT_TOKENS=16 \
  DIRECT_TARGET=https://qwen3-kserve-workload-svc.ai-tenant-site-b.svc.cluster.local:8000 MODEL=Qwen3-Coder-30B-A3B \
  ./bench.sh direct long > ~/.local/share/grid-bench/sD-warm.log 2>&1
echo "warm_end $(date -u +%FT%TZ)" >> $EV
grep -E '^\| (direct|constant)' ~/.local/share/grid-bench/sD-warm.log | head -6 >> $EV
# The burst: new conversations sharing that prompt, through the front door.
echo "burst_start $(date -u +%FT%TZ)" >> $EV
LABEL=sD-sticky-burst PREFIX_TOKENS=7500 COUNT=300 RATES=5 STEP_SECONDS=30 MAX_CONCURRENCY=256 \
  ./bench.sh front long > ~/.local/share/grid-bench/sD-burst.log 2>&1
echo "burst_end $(date -u +%FT%TZ)" >> $EV
d=$(ls -td ~/.local/share/grid-bench/gridbench-*-sd-sticky-burst | head -1)
python3 sites_report.py $d --window 5 | sed -n '/Share in/,$p' | head -12 >> $EV
grep -E '^\| (front|constant)' ~/.local/share/grid-bench/sD-burst.log | head -6 >> $EV
cat $EV
