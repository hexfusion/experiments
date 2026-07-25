#!/bin/bash
# Adds the ext_proc emulation layer and a Praxis data plane to the cluster that
# 03-kind-raw brings up. Run `make 03-up` first: this reuses its EPP, sims,
# InferencePool and CRDs rather than standing up a second stack.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXP="$(cd "$DIR/.." && pwd)"
CLUSTER="${CLUSTER:-llm-d-03}"
NS=llm-d

command -v podman >/dev/null || { echo "podman required"; exit 1; }
kubectl config use-context "kind-${CLUSTER}" >/dev/null

echo "== building the gateway image =="
podman build -t localhost/epp-http-gateway:dev -f "$DIR/Containerfile.gateway" "$EXP"

echo "== building praxis with the out-of-tree filter =="
# Built on the host because the crate depends on sibling paths outside any
# single build context.
( cd "$EXP/praxis-epp-e2e" && cargo build --release )
cp "$EXP/praxis-epp-e2e/target/release/praxis-epp" "$DIR/praxis-epp"
podman build -t localhost/praxis-epp:dev -f "$DIR/Containerfile.praxis" "$DIR"
rm -f "$DIR/praxis-epp"

echo "== side-loading images (rootless podman: kind cannot pull localhost) =="
for img in epp-http-gateway praxis-epp; do
  podman save -o "/tmp/${img}.tar" "localhost/${img}:dev"
  kind load image-archive "/tmp/${img}.tar" --name "$CLUSTER"
  rm -f "/tmp/${img}.tar"
done

echo "== applying =="
kubectl apply -f "$DIR/manifests/"
kubectl -n "$NS" rollout status deploy/epp-http-gateway --timeout=120s
kubectl -n "$NS" rollout status deploy/praxis-epp --timeout=120s

echo "== port-forward praxis -> localhost:18081 =="
pkill -f "kubectl.*port-forward.*svc/praxis-epp" 2>/dev/null || true
nohup kubectl port-forward -n "$NS" svc/praxis-epp 18081:8080 >/tmp/praxis-pf.log 2>&1 &
sleep 2
echo
echo "ready. send a request through Praxis:"
echo "  curl -s localhost:18081/v1/chat/completions -H 'content-type: application/json' \\"
echo "    -d '{\"model\":\"food-review\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'"
