#!/usr/bin/env bash
# Load a locally built image into the kind clusters.
#
# kind here runs under rootful podman, so this needs sudo and cannot run
# unattended. The archive is built by whoever ran the image build.
#
#   ./load-image.sh .generated/grid-ai-rollup-load.tar
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCHIVE="${1:-${HERE}/.generated/grid-ai-rollup-load.tar}"
[ -f "$ARCHIVE" ] || { echo "no archive at ${ARCHIVE}" >&2; exit 1; }

for c in pool-a pool-b pool-c; do
  echo "loading into grid-llmd-pm-${c}"
  sudo -E env PATH="$PATH" CONTAINER_HOST=unix:///run/podman/podman.sock \
    KIND_EXPERIMENTAL_PROVIDER=podman \
    kind load image-archive "$ARCHIVE" --name "grid-llmd-pm-${c}"
done
echo "done"
