#!/bin/bash
set -euo pipefail

NS="${NS:-llm-d-praxis}"
REGISTRY_HOST="${REGISTRY_HOST:-default-route-openshift-image-registry.apps.dagobah.hexfusion.local}"
TAG="${TAG:-v0.3.1}"
IMAGE_LOCAL="praxis:${TAG}"
IMAGE_REMOTE="${REGISTRY_HOST}/${NS}/praxis:${TAG}"

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

if ! oc whoami >/dev/null 2>&1; then
  echo "ERROR: not logged in to OCP (oc login required)" >&2
  exit 1
fi

oc apply -f cluster/manifests/00-namespace.yaml >/dev/null

echo "==> building ${IMAGE_LOCAL}"
podman build \
  --build-arg PRAXIS_REF="${TAG}" \
  -t "${IMAGE_LOCAL}" \
  -f praxis/Containerfile praxis/

echo "==> pushing ${IMAGE_REMOTE}"
podman tag "${IMAGE_LOCAL}" "${IMAGE_REMOTE}"
TOKEN=$(oc -n "${NS}" create token builder --duration=1h)
podman login --tls-verify=false -u builder -p "${TOKEN}" "${REGISTRY_HOST}" >/dev/null
podman push --tls-verify=false "${IMAGE_REMOTE}"

echo
echo "OK  ${IMAGE_REMOTE}"
echo "    pulled in-cluster as: image-registry.openshift-image-registry.svc:5000/${NS}/praxis:${TAG}"
