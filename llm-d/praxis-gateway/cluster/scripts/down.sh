#!/bin/bash
set -euo pipefail

NS="${NS:-llm-d-praxis}"

if kubectl get ns "$NS" >/dev/null 2>&1; then
  kubectl delete ns "$NS" --wait=true
else
  echo "namespace $NS not found, nothing to do"
fi
