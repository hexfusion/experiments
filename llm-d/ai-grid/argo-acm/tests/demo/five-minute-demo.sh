#!/bin/bash
KC=~/.kube/grid.kubeconfig; KS="kubectl --kubeconfig $KC --context site-d"
LOG=$1; KEY=$(tr -d '\r\n' < ~/.config/grid-bench-maas-key)
ts() { date -u +%H:%M:%S; }
echo "$(ts) T0 baseline: front-door stream starts" >> $LOG
( for i in $(seq 1 165); do for j in 1 2 3 4 5 6; do curl -sk --max-time 90 --resolve maas.acme.lab:443:192.168.1.201 -o /dev/null -H "Authorization: Bearer $KEY" -H 'content-type: application/json' -d "{\"model\":\"qwen3-coder-30b-a3b\",\"messages\":[{\"role\":\"user\",\"content\":\"Demo $i-$j: write four sentences about the ocean and its tides.\"}],\"max_tokens\":96}" https://maas.acme.lab/v1/chat/completions & done; sleep 2; done; wait ) >/dev/null 2>&1 &
FD=$!
sleep 60
echo "$(ts) T1 extra load inside Portland: 24 streams at vLLM 10.42.0.31:8000 for 90s" >> $LOG
$KS -n llm-d exec demo-load -- sh -c 'end=$(( $(date +%s) + 90 )); for w in $(seq 1 24); do ( while [ $(date +%s) -lt $end ]; do curl -s --max-time 60 -o /dev/null -H "content-type: application/json" -d "{\"model\":\"Qwen3-Coder-30B-A3B\",\"messages\":[{\"role\":\"user\",\"content\":\"Local load $w: write a long essay about the history of harbors.\"}],\"max_tokens\":256}" http://10.42.0.31:8000/v1/chat/completions; done ) & done; wait' >/dev/null 2>&1 &
LL=$!
sleep 90
echo "$(ts) T2 local load ended; Portland goes dark: EPP scaled to 0" >> $LOG
$KS -n llm-d scale deploy/optimized-baseline-epp --replicas=0 >> $LOG 2>&1
sleep 60
echo "$(ts) T3 Portland comes back: EPP scaled to 1" >> $LOG
$KS -n llm-d scale deploy/optimized-baseline-epp --replicas=1 >> $LOG 2>&1
$KS -n llm-d rollout status deploy/optimized-baseline-epp --timeout=120s >> $LOG 2>&1
sleep 90
echo "$(ts) T4 end; waiting for the front-door stream to drain" >> $LOG
wait $FD $LL 2>/dev/null
echo "$(ts) done" >> $LOG
