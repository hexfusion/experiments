#!/usr/bin/env bash
# Inject the credential after the pick. IPP-post maps the EPP's chosen endpoint -> that cluster's pool
# token (from Secret pool-credentials) and sets Authorization. Needs the spoke-and-hub-credential-injector
# IPP build: set IPP_IMAGE to your push (a local image is loaded into the hub nodes automatically).
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
need kind; need kubectl

NS="${NS:-llm-d}"
PORT="${PORT:-8000}"          # InferencePool targetPort = the port the EPP reports
IPP_IMAGE="${IPP_IMAGE:-}"

k "$HUB" -n "$NS" get secret pool-credentials >/dev/null 2>&1 || die "Secret ${NS}/pool-credentials missing -- run 3-auth.sh first"

# the EPP's candidate endpoints are the cluster reverse-proxy pod IPs
ip() { k "$HUB" -n "$NS" get pod -l "app=hub-pool,cluster=$1" -o jsonpath='{.items[0].status.podIP}'; }
A="$(ip cluster-a)"; B="$(ip cluster-b)"
[ -n "$A" ] && [ -n "$B" ] || die "proxy pod IPs not found (run 2-hub.sh)"
log "endpoints cluster-a=$A cluster-b=$B (port $PORT)"

# render endpoint->cluster map (both bare-IP and IP:port, to match whatever the EPP emits)
RENDER="$(mktemp)"; trap 'rm -f "$RENDER"' EXIT
sed -e "s|__ENDPOINT_A_PORT__|${A}:${PORT}|g" -e "s|__ENDPOINT_A__|${A}|g" \
    -e "s|__ENDPOINT_B_PORT__|${B}:${PORT}|g" -e "s|__ENDPOINT_B__|${B}|g" "$HERE/manifests/hub/ipp.yaml" > "$RENDER"
if [ -n "$IPP_IMAGE" ]; then
  log "IPP image: $IPP_IMAGE"
  sed -i "s|image: ghcr.io/llm-d/llm-d-inference-payload-processor:main|image: ${IPP_IMAGE}|" "$RENDER"
  podman image exists "$IPP_IMAGE" 2>/dev/null && kind load docker-image "$IPP_IMAGE" --name "$HUB"
fi

k "$HUB" apply -f "$RENDER"
k "$HUB" apply -f "$HERE/manifests/hub/ipp-envoyfilter.yaml"   # INSERT_AFTER the EPP
k "$HUB" -n "$NS" rollout status deploy/payload-processor --timeout=150s

log "ipp up. verify: ipp_credential_injection_total{result=\"matched\"} rises under benchmark/run.sh"
