#!/usr/bin/env bash
cd "$(dirname "$0")/.."
K="kubectl --kubeconfig $HOME/.kube/grid.kubeconfig --context dagobah"
EV=~/.local/share/grid-bench/s3-events.txt; : > $EV
probe() { $K -n grid exec deploy/grid-gateway -c praxis -- sh -c '
  for t in "dns:getent hosts maas-api.redhat-ai-gateway-infra.svc.cluster.local" \
           "incluster:wget -q -T 3 --no-check-certificate -O /dev/null https://qwen3-kserve-workload-svc.ai-tenant-site-a.svc.cluster.local:8000/health" \
           "factory8080:wget -q -T 3 -O /dev/null http://192.168.1.150:8080/" \
           "factory9091:wget -q -T 3 -O /dev/null http://192.168.1.150:9091/"; do
    n=${t%%:*}; c=${t#*:}; s=$(date +%s.%N); out=$(sh -c "$c" 2>&1); rc=$?; e=$(date +%s.%N)
    echo "$n rc=$rc secs=$(awk -v a=$s -v b=$e "BEGIN{printf \"%.1f\", b-a}") $(echo "$out" | head -1 | cut -c1-80)"
  done' 2>&1; }
(LABEL=s3-signal-loss RATES=3 STEP_SECONDS=480 MAX_CONCURRENCY=256 COUNT=3000 ./bench.sh front long > ~/.local/share/grid-bench/s3.log 2>&1) &
bench=$!
echo "== baseline probe $(date -u +%T)" >> $EV; probe >> $EV
sleep 90
$K apply -f block-factory-signals.yaml >/dev/null && echo "netpol_applied $(date -u +%FT%TZ)" >> $EV
(sleep 360; $K -n grid delete networkpolicy bench-block-factory-signals --ignore-not-found >/dev/null 2>&1 && echo "netpol_autodelete_check $(date -u +%FT%TZ)" >> $EV) &
sleep 5; echo "== blocked probe $(date -u +%T)" >> $EV; probe >> $EV
sleep 295
$K -n grid delete networkpolicy bench-block-factory-signals >/dev/null && echo "netpol_deleted $(date -u +%FT%TZ)" >> $EV
sleep 5; echo "== restored probe $(date -u +%T)" >> $EV; probe >> $EV
wait $bench
grep -E '^\| (front|constant)' ~/.local/share/grid-bench/s3.log | head -8; cat $EV
