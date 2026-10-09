#!/bin/bash
KC=~/.kube/grid.kubeconfig; KS="kubectl --kubeconfig $KC --context site-d"
LOG=$1; KEY=$(tr -d '\r\n' < ~/.config/grid-bench-maas-key)
ts() { date -u +%H:%M:%S; }
echo "$(ts) T0 baseline: front-door stream starts (6 req / 2 s, Qwen3-Coder)" >> $LOG
( for i in $(seq 1 150); do for j in 1 2 3 4 5 6; do curl -sk --max-time 90 --resolve maas.acme.lab:443:192.168.1.201 -o /dev/null -H "Authorization: Bearer $KEY" -H 'content-type: application/json' -d "{\"model\":\"qwen3-coder-30b-a3b\",\"messages\":[{\"role\":\"user\",\"content\":\"Demo $i-$j: write four sentences about the ocean and its tides.\"}],\"max_tokens\":96}" https://maas.acme.lab/v1/chat/completions & done; sleep 2; done; wait ) >/dev/null 2>&1 &
FD=$!
sleep 60
echo "$(ts) T1 extra load inside Portland: ${STREAMS:-24} streams at vLLM for 90 s" >> $LOG
$KS -n llm-d exec -i demo-load -- sh -s -- "${STREAMS:-24}" 90 10.42.0.31:8000 < "$(dirname "$0")/local-load.sh" >/dev/null 2>&1 &
LL=$!
sleep 90
echo "$(ts) T2 local load ended; nothing else changes: watch the rebalance" >> $LOG
sleep 150
echo "$(ts) T3 end; front-door stream draining" >> $LOG
wait $FD $LL 2>/dev/null
echo "$(ts) done" >> $LOG
