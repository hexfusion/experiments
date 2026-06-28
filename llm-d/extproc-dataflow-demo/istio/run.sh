#!/usr/bin/env bash
# Validate the IPP-brackets-EPP ext_proc chain on ISTIO (the data plane RHAII ships: GatewayClass=istio,
# Envoy). Same mock binary as the raw-Envoy demo; the chain is expressed via Istio EnvoyFilter.
# Stands up a kind cluster, installs Istio + Gateway API, deploys the mocks, applies the EnvoyFilter,
# and sends a request through the gateway.
set -uo pipefail
cd "$(dirname "$0")"

CLUSTER=extproc-istio
export KIND_EXPERIMENTAL_PROVIDER=podman
ISTIO_VER=1.28.1
GWAPI_VER=v1.2.1
ISTIOCTL="${ISTIOCTL:-/tmp/istioctl}"

step() { echo; echo "== $* =="; }

# kind under rootless podman needs a delegated systemd user scope (Delegate=yes).
kind_run() {
  if command -v systemd-run >/dev/null 2>&1; then
    systemd-run --user --scope -q -p "Delegate=yes" kind "$@"
  else
    kind "$@"
  fi
}

step "ensure istioctl ($ISTIO_VER)"
if ! "$ISTIOCTL" version --remote=false >/dev/null 2>&1; then
  curl -fsSL "https://github.com/istio/istio/releases/download/${ISTIO_VER}/istioctl-${ISTIO_VER}-linux-amd64.tar.gz" | tar xz -C /tmp
  ISTIOCTL=/tmp/istioctl
fi

step "build mock binary (host, static) + image"
( cd .. && CGO_ENABLED=0 GOOS=linux go build -o istio/extproc-demo ./. ) || exit 1
podman build -t extproc-demo:latest -f Dockerfile . || exit 1

step "kind cluster $CLUSTER"
if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  kind_run create cluster --config kind.yaml || exit 1
fi
KCTX="kind-$CLUSTER"

step "load image into kind"
podman save extproc-demo:latest -o /tmp/extproc-demo.tar && kind_run load image-archive /tmp/extproc-demo.tar --name "$CLUSTER" || exit 1

step "Gateway API CRDs ($GWAPI_VER)"
kubectl --context "$KCTX" apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VER}/standard-install.yaml" || exit 1

step "install Istio (minimal)"
"$ISTIOCTL" --context "$KCTX" install -y --set profile=minimal || exit 1

step "deploy mocks"
kubectl --context "$KCTX" apply -f manifests/mock.yaml
kubectl --context "$KCTX" -n extproc-demo rollout status deploy/ipp --timeout=90s
kubectl --context "$KCTX" -n extproc-demo rollout status deploy/epp --timeout=90s
kubectl --context "$KCTX" -n extproc-demo rollout status deploy/echo --timeout=90s

step "gateway + route + EnvoyFilter ext_proc chain"
kubectl --context "$KCTX" apply -f manifests/gateway.yaml
kubectl --context "$KCTX" apply -f manifests/extproc.yaml
echo "waiting for gateway deployment extproc-gw-istio..."
for i in $(seq 1 30); do
  kubectl --context "$KCTX" -n extproc-demo get deploy extproc-gw-istio >/dev/null 2>&1 && break; sleep 2
done
kubectl --context "$KCTX" -n extproc-demo rollout status deploy/extproc-gw-istio --timeout=120s

step "port-forward gateway :80 -> localhost:18080"
kubectl --context "$KCTX" -n extproc-demo port-forward svc/extproc-gw-istio 18080:80 >/tmp/istio-pf.log 2>&1 &
PF=$!; trap 'kill $PF 2>/dev/null || true' EXIT
sleep 4

req() { # tenant model
  step "request through the Istio gateway: tenant=$1 model=$2"
  curl -s --max-time 15 -o /tmp/istio-resp.json -w "HTTP %{http_code} (%{time_total}s)\n" \
    -X POST localhost:18080/v1/chat/completions \
    -H 'content-type: application/json' -H "x-tenant: $1" \
    -d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" || true
  echo "-- as the upstream (echo) saw it --"
  jq '{provider: .headers["x-ipp-provider"], model: .headers["x-gateway-model-name"], authorization: .headers.authorization, body: (.body|fromjson)}' /tmp/istio-resp.json 2>/dev/null || cat /tmp/istio-resp.json
}
req acme   gpt-4      # OpenAI-compatible: auth only, no body buffer
req globex claude-3   # different API: auth + body translated

step "ext_proc dataflow (mock logs)"
for r in ipp epp; do echo "--- $r ---"; kubectl --context "$KCTX" -n extproc-demo logs deploy/$r --tail=10 2>/dev/null; done

step "gateway filter chain (proof the three ext_proc filters are wired in order)"
GW=$(kubectl --context "$KCTX" -n extproc-demo get pod -l istio.io/gateway-name=extproc-gw -o name | head -1)
"$ISTIOCTL" --context "$KCTX" -n extproc-demo proxy-config listener "${GW#pod/}" -o json 2>/dev/null \
  | grep -oE 'ext_proc\.[a-z_]+' | sort -u || echo "(could not dump listener)"
