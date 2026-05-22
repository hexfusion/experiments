#!/bin/bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

NS=llm-d-praxis

if ! kubectl auth can-i get nodes >/dev/null 2>&1; then
  echo "ERROR: not logged in to cluster" >&2
  echo "       Re-login: oc login --server=https://api.dagobah.hexfusion.local:6443" >&2
  exit 1
fi

kubectl apply -f manifests/00-namespace.yaml
../../scripts/ensure-hf-token.sh "$NS"

echo "==> applying manifests"
kubectl apply -f manifests/

echo "==> waiting for vLLM (first run pulls model, can take several minutes)"
kubectl -n "$NS" rollout status deployment/vllm --timeout=600s

echo "==> waiting for praxis"
kubectl -n "$NS" rollout status deployment/praxis --timeout=120s

echo
echo "OK"
echo "  Service: praxis.${NS}.svc:8080"
echo "  Route:   https://\$(kubectl -n ${NS} get route praxis -o jsonpath='{.spec.host}')"
echo "  test:    ./scripts/test.sh"
