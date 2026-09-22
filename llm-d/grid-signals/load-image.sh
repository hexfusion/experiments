#!/usr/bin/env bash
# Load locally built images into the kind clusters.
#
# kind here runs under rootful podman, so this needs sudo and cannot run
# unattended. Give it image references; each is saved and loaded into all
# three clusters.
#
#   ./load-image.sh quay.io/sbatsche/grid-operator:geo-abc1234
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${HERE}/.generated"
mkdir -p "$OUT"
[ "$#" -gt 0 ] || { sed -n '2,7p' "$0"; exit 2; }

for image in "$@"; do
  archive="${OUT}/$(echo "$image" | tr '/:' '__').tar"
  echo "saving ${image}"
  podman save -o "$archive" "$image" >/dev/null
  for c in pool-a pool-b pool-c; do
    echo "  -> grid-llmd-pm-${c}"
    sudo -E env PATH="$PATH" CONTAINER_HOST=unix:///run/podman/podman.sock \
      KIND_EXPERIMENTAL_PROVIDER=podman \
      kind load image-archive "$archive" --name "grid-llmd-pm-${c}"
  done
done
echo "done"
