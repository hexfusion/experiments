#!/usr/bin/env bash
cd "$(dirname "$0")/.."
K="kubectl --kubeconfig $HOME/.kube/grid.kubeconfig --context dagobah"
out=~/.local/share/grid-bench/gridbench-$(date +%Y%m%d-%H%M%S)-burst; mkdir -p $out
python3 sites_sampler.py $out/sites.jsonl & sp=$!
$K -n grid-load delete job grid-burst --ignore-not-found >/dev/null; date +%s > $out/start
$K apply -f burst-job.yaml >/dev/null
until $K -n grid-load logs job/grid-burst --tail=2 2>/dev/null | grep -q 'BURST DONE'; do sleep 10; done
date +%s > $out/end; kill $sp
$K -n grid-load logs job/grid-burst | grep -v 'BURST DONE' > $out/requests.csv
$K -n grid-load delete job grid-burst --wait=false >/dev/null
echo "$out"; tail -n +2 $out/requests.csv | cut -d, -f3 | sort | uniq -c
