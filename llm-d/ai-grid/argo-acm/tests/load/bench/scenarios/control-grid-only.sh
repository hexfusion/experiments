#!/usr/bin/env bash
# Grid-only control: hq's grid gateway via port-forward, bypassing MaaS. Same shape and seed as scenario 1.
cd "$(dirname "$0")/.."
K="kubectl --kubeconfig $HOME/.kube/grid.kubeconfig --context dagobah"
out=~/.local/share/grid-bench/gridbench-$(date +%Y%m%d-%H%M%S)-${LABEL:-control-grid-only}; mkdir -p $out
python3 gen_data.py $out/data.jsonl 512 256 4000
$K -n grid port-forward deploy/grid-gateway 18443:8080 > $out/pf.log 2>&1 & pf=$!
sleep 3
python3 sites_sampler.py $out/sites.jsonl & sp=$!
date +%s > $out/start
guidellm run --disable-progress \
  --backend "kind=openai_http,target=https://localhost:18443,model=Qwen3-Coder-30B-A3B,verify=false,validate_backend=false,timeout=300" \
  --profile "${PROFILE:-{\"kind\":\"concurrent\",\"streams\":[8,16,32]}}" \
  --constraint "kind=max_duration,seconds=${STEP_SECONDS:-60}" \
  --data "{\"kind\":\"json_file\",\"path\":\"$out/data.jsonl\",\"load_kwargs\":{\"split\":\"train\"}}" \
  --output kind=json,path=$out/guidellm.json > $out/guidellm.log 2>&1
echo "guidellm rc=$?"
date +%s > $out/end
kill $sp $pf 2>/dev/null || true
python3 summarize.py $out/guidellm.json control | tee $out/summary.md
python3 sites_report.py $out | tee -a $out/summary.md | head -20
echo $out
