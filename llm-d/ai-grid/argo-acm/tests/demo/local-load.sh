# runs inside the demo-load pod: sh -s -- <streams> <seconds> <vllm addr>
n=$1; secs=$2; addr=$3
end=$(( $(date +%s) + secs ))
w=1
while [ $w -le $n ]; do
  ( while [ $(date +%s) -lt $end ]; do
      curl -s --max-time 60 -o /dev/null -H "content-type: application/json" \
        -d "{\"model\":\"Qwen3-Coder-30B-A3B\",\"messages\":[{\"role\":\"user\",\"content\":\"Local load $w: write a long essay about the history of harbors.\"}],\"max_tokens\":256}" \
        "http://$addr/v1/chat/completions"
    done ) &
  w=$((w+1))
done
wait
