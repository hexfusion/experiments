#!/usr/bin/env bash
# Each spoke = a normal llm-d serving cluster: istio + GIE CRDs + inference-sim (Model A) + spoke EPP
# (picks a POD) + spoke Gateway. The spoke EPP also publishes the pool aggregate the hub scrapes.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
need kind; need kubectl; need podman; need istioctl

NS="${NS:-llm-d}"
SIM_IMAGE="${SIM_IMAGE:-ghcr.io/llm-d/llm-d-inference-sim:v0.10.0}"
EPP_IMAGE="${EPP_IMAGE:-ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0}"
GIE_REF="${GIE_REF:-v1.0.0}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"

for img in "$SIM_IMAGE" "$EPP_IMAGE"; do
  podman image exists "$img" || { log "pulling $img"; podman pull -q "$img"; }
done

for s in "${SPOKES[@]}"; do
  ctx="$(ctx "$s")"
  log "===== [$s] ====="

  istioctl install --context "$ctx" --set profile=default \
    --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true -y >/dev/null
  k "$s" -n istio-system rollout status deploy/istiod --timeout=180s

  k "$s" get crd gateways.gateway.networking.k8s.io >/dev/null 2>&1 \
    || k "$s" apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml" >/dev/null
  k "$s" get crd inferencepools.inference.networking.k8s.io >/dev/null 2>&1 \
    || k "$s" apply -k "https://github.com/kubernetes-sigs/gateway-api-inference-extension/config/crd?ref=${GIE_REF}" >/dev/null

  for img in "$SIM_IMAGE" "$EPP_IMAGE"; do kind load docker-image "$img" --name "$s"; done

  k "$s" create namespace "$NS" --dry-run=client -o yaml | k "$s" apply -f - >/dev/null
  k "$s" apply -f "$HERE/manifests/spoke/inference-sim.yaml"
  k "$s" apply -f "$HERE/router/spoke-epp.yaml"
  k "$s" apply -f "$HERE/manifests/spoke/spoke-pool.yaml"
  k "$s" apply -f "$HERE/manifests/spoke/spoke-gateway.yaml"
  k "$s" -n "$NS" rollout status deploy/inference-sim --timeout=180s
  k "$s" -n "$NS" rollout status deploy/spoke-epp --timeout=120s

  log "[$s] gateway: $(k "$s" -n "$NS" get gateway spoke -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || echo '<pending>')"
done

log "spokes up. next: scripts/2-hub.sh"
