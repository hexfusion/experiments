#!/usr/bin/env bash
cd "$(dirname "$0")/.."
K="kubectl --kubeconfig $HOME/.kube/grid.kubeconfig --context site-d"
EV=~/.local/share/grid-bench/s2-events.txt; : > $EV
(LABEL=s2-factory-saturated RATES=3 STEP_SECONDS=480 MAX_CONCURRENCY=256 COUNT=3000 ./bench.sh front long > ~/.local/share/grid-bench/s2.log 2>&1) &
bench=$!
sleep 90
$K -n llm-d delete job factory-overload --ignore-not-found >/dev/null; $K apply -f factory-overload.yaml >/dev/null; echo "overload_applied $(date -u +%FT%TZ)" >> $EV
wait $bench
P=$($K -n llm-d get pods -l job-name=factory-overload -o name | head -1)
$K -n llm-d logs $P | tail -3 >> $EV
$K -n llm-d exec $P -- cat /tmp/out.json > ~/.local/share/grid-bench/s2-overload.json 2>/dev/null
$K -n llm-d delete job factory-overload --wait=false >/dev/null; echo "overload_deleted $(date -u +%FT%TZ)" >> $EV
grep -E '^\| (front|constant)' ~/.local/share/grid-bench/s2.log | head -8; cat $EV
