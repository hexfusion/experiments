#!/usr/bin/env bash
# The hub = an EPP whose "pods" are clusters. A reverse proxy per cluster IS the endpoint the hub EPP
# picks; the proxy forwards inference to that spoke's Gateway and passes the spoke EPP's aggregate
# /metrics back (the cluster summary the hub scores on). Also brings up the round-robin baseline.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
need kind; need kubectl; need podman; need istioctl

NS="${NS:-llm-d}"
EPP_IMAGE="${EPP_IMAGE:-ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0}"
PROXY_IMAGE="${PROXY_IMAGE:-nginx:1.27-alpine}"
GIE_REF="${GIE_REF:-v1.0.0}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"
HUB_CTX="$(ctx "$HUB")"

# istio (hub ext_proc data plane)
istioctl install --context "$HUB_CTX" --set profile=default \
  --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true -y >/dev/null
k "$HUB" -n istio-system rollout status deploy/istiod --timeout=180s

# CRDs (Gateway API + GIE)
k "$HUB" get crd gateways.gateway.networking.k8s.io >/dev/null 2>&1 \
  || k "$HUB" apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml" >/dev/null
k "$HUB" get crd inferencepools.inference.networking.k8s.io >/dev/null 2>&1 \
  || k "$HUB" apply -k "https://github.com/kubernetes-sigs/gateway-api-inference-extension/config/crd?ref=${GIE_REF}" >/dev/null

# images into the hub nodes
for img in "$PROXY_IMAGE" "$EPP_IMAGE"; do
  podman image exists "$img" || { log "pulling $img"; podman pull -q "$img"; }
  kind load docker-image "$img" --name "$HUB"
done

# discover each spoke's Gateway (inference) + spoke-epp-metrics LB (cluster summary), from 1-spokes
gw()  { k "$1" -n "$NS" get gateway spoke -o jsonpath='{.status.addresses[0].value}'; }
epp() { k "$1" -n "$NS" get svc spoke-epp-metrics -o jsonpath='{.status.loadBalancer.ingress[0].ip}'; }
GW_A="$(gw "$SPOKE_A")"; EPP_A="$(epp "$SPOKE_A")"
GW_B="$(gw "$SPOKE_B")"; EPP_B="$(epp "$SPOKE_B")"
GW_C="$(gw "$SPOKE_C")"; EPP_C="$(epp "$SPOKE_C")"
for v in GW_A EPP_A GW_B EPP_B GW_C EPP_C; do [ -n "${!v}" ] || die "could not resolve $v (run 1-spokes.sh first)"; done
log "spoke-a gw=$GW_A epp=$EPP_A | spoke-b gw=$GW_B epp=$EPP_B | spoke-c gw=$GW_C epp=$EPP_C"

# apply the hub layer
k "$HUB" create namespace "$NS" --dry-run=client -o yaml | k "$HUB" apply -f - >/dev/null
sed -e "s|__GW_A__|$GW_A|g" -e "s|__EPP_A__|$EPP_A|g" -e "s|__GW_B__|$GW_B|g" -e "s|__EPP_B__|$EPP_B|g" \
    -e "s|__GW_C__|$GW_C|g" -e "s|__EPP_C__|$EPP_C|g" \
  "$HERE/manifests/hub/cluster-proxies.yaml" | k "$HUB" apply -f -
sed "s|image: ghcr.io/llm-d/llm-d-router.*|image: ${EPP_IMAGE}|" "$HERE/router/hub-epp.yaml" | k "$HUB" apply -f -
k "$HUB" apply -f "$HERE/manifests/hub/hub-pool.yaml"
k "$HUB" apply -f "$HERE/manifests/hub/hub-gateway.yaml"
k "$HUB" apply -f "$HERE/manifests/hub/baseline-rr.yaml"   # round-robin control (hub-rr Gateway -> Service)

k "$HUB" -n "$NS" rollout status deploy/cluster-a --timeout=120s
k "$HUB" -n "$NS" rollout status deploy/cluster-b --timeout=120s
k "$HUB" -n "$NS" rollout status deploy/cluster-c --timeout=120s
k "$HUB" -n "$NS" rollout status deploy/epp --timeout=120s

log "hub: $(k "$HUB" -n "$NS" get gateway hub -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || echo '<pending>')"
log "hub up. next: scripts/3-auth.sh (optional), then benchmark/run.sh"
