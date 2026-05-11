#!/bin/bash
set -euo pipefail
CLUSTER=llm-d-03
PF_PID_FILE=/tmp/llm-d-03-portforward.pid

if [[ -f "$PF_PID_FILE" ]]; then
  pid=$(cat "$PF_PID_FILE")
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid"
    echo "Killed port-forward PID $pid"
  fi
  rm -f "$PF_PID_FILE"
fi

kind delete cluster --name "$CLUSTER"
