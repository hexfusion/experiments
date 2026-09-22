#!/usr/bin/env bash
# Two-route topology on kind: the decider is a routable backend, not a filter.
#
#   client -> gateway -> route 1 (no marker)      -> IPP
#   IPP    -> gateway -> route 2 (marker present) -> ORIGINAL_DST -> sim
#
# Loop avoidance is Gateway API match precedence, not header negation, because
# the spec cannot express "header absent". Nothing in the data plane runs
# ext_proc.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER="${CLUSTER:-two-route}"
NS=two-route
EPP_SRC="${EPP_SRC:-$HOME/projects/llm-d/.worktrees/llm-d-router/epp-http-transport}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"
GIE_REF="${GIE_REF:-v1.0.0}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

docker --version 2>/dev/null | grep -qi podman && export KIND_EXPERIMENTAL_PROVIDER=podman

# Rootless podman needs systemd Delegate=yes or kind cannot own the cluster cgroup.
kind_run() {
  if [ "${KIND_EXPERIMENTAL_PROVIDER:-}" = podman ] && command -v systemd-run >/dev/null 2>&1; then
    systemd-run --user --scope -q -p "Delegate=yes" kind "$@"
  else
    kind "$@"
  fi
}
k() { kubectl --context "kind-$CLUSTER" "$@"; }

log "building the binary and image"
( cd "$HERE/.." && CGO_ENABLED=0 go build -trimpath -o two-route-kind/two-route ./two-route-kind/cmd )
podman build -q -t localhost/two-route:dev -f "$HERE/Containerfile" "$HERE" >/dev/null

# EPP carries the plain-HTTP transport, which is not upstream. Build it from the
# worktree if the binary is not already staged here.
if [ ! -x "$HERE/epp/epp" ]; then
  [ -d "$EPP_SRC" ] || die "no EPP binary at $HERE/epp/epp and no worktree at $EPP_SRC"
  log "building epp from $EPP_SRC"
  ( cd "$EPP_SRC" && GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -o "$HERE/epp/epp" ./cmd/epp )
fi
podman build -q -t localhost/epp-shim:dev -f "$HERE/epp/Containerfile" "$HERE/epp" >/dev/null

if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  log "creating cluster $CLUSTER"
  # Single node: rootless podman cannot join workers, and kind's control plane is schedulable.
  kind_run create cluster --name "$CLUSTER" --wait 120s --config=- <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: $CLUSTER
nodes:
  - role: control-plane
networking:
  podSubnet: "10.40.0.0/16"
  serviceSubnet: "10.140.0.0/16"
EOF
fi

log "loading image"
for img in localhost/two-route:dev localhost/epp-shim:dev; do
  kind_run load docker-image "$img" --name "$CLUSTER" >/dev/null
done

log "installing Gateway API CRDs and istio"
k get crd gateways.gateway.networking.k8s.io >/dev/null 2>&1 || \
  k apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml" >/dev/null
istioctl install --context "kind-$CLUSTER" --set profile=minimal -y >/dev/null
k -n istio-system rollout status deploy/istiod --timeout=180s >/dev/null

log "sims"
k apply -f "$HERE/manifests/00-namespace.yaml" >/dev/null
k apply -f "$HERE/manifests/10-sims.yaml" >/dev/null
for s in a b c; do k -n $NS rollout status "deploy/sim-$s" --timeout=120s >/dev/null; done

log "gateway, routes, original-dst"
k apply -f "$HERE/manifests/30-gateway.yaml" >/dev/null
k apply -f "$HERE/manifests/40-original-dst.yaml" >/dev/null
# Not waiting on Programmed: kind has no LoadBalancer IP so that condition never
# goes true. The data path is in-cluster and unaffected.
k -n $NS rollout status deploy/demo-istio --timeout=180s >/dev/null

log "epp with the http transport"
k get crd inferencepools.inference.networking.k8s.io >/dev/null 2>&1 || \
  k apply -k "https://github.com/kubernetes-sigs/gateway-api-inference-extension/config/crd?ref=${GIE_REF}" >/dev/null
k apply -f "$HERE/manifests/50-epp.yaml" >/dev/null
k -n $NS rollout status deploy/epp --timeout=180s >/dev/null

# Both addresses are resolved here rather than left as DNS names. ORIGINAL_DST
# reads an address from the header so it cannot take a name at all, and the
# gateway name does not resolve reliably from a pod on this setup, which is the
# same DNS-pollution gotcha the spoke-and-hub kind poc works around. Pod IPs for
# the sims, because a picker names pods, not services.
ENDPOINTS="$(for s in a b c; do
  printf '%s:8000,' "$(k -n $NS get pod -l "app=sim-$s" -o jsonpath='{.items[0].status.podIP}')"
done | sed 's/,$//')"
GW="$(k -n $NS get svc demo-istio -o jsonpath='{.spec.clusterIP}')"
EPP="$(k -n $NS get svc epp -o jsonpath='{.spec.clusterIP}')"
[ -n "$ENDPOINTS" ] && [ -n "$GW" ] && [ -n "$EPP" ] || die "could not resolve sims, gateway or epp"
log "gateway=$GW epp=$EPP endpoints=$ENDPOINTS"

log "ipp"
sed -e "s|__ENDPOINTS__|$ENDPOINTS|" -e "s|__GATEWAY__|http://$GW:80|" -e "s|__EPP__|http://$EPP:9100|" \
  "$HERE/manifests/20-ipp.yaml" | k apply -f - >/dev/null
k -n $NS rollout status deploy/ipp --timeout=120s >/dev/null

log "up. verify with: $HERE/verify.sh"
