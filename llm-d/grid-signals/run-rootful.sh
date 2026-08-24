#!/usr/bin/env bash
# Run the demo against rootful podman.
#
# kind needs a runtime it can delegate cgroups through. Rootless podman on this
# host reports Delegate=no until the login session is refreshed, so this points
# everything at the system socket instead.
#
# xtask is invoked as a prebuilt binary rather than through cargo, so root does
# not write into the user's cargo target and break the next build there.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GRID_REPO="${GRID_REPO:-$HOME/projects/praxis-proxy/.worktrees/grid/federation-endpoint}"

# Rebuild rather than check for existence. A binary from before the last change
# runs happily and silently does the old thing, which is how a three-cluster
# topology came up with two.
( cd "${GRID_REPO}" && cargo build -p xtask ) || exit 1

# Always regenerate. Reusing whatever was there last time is how a topology fix
# lands in the repo and the run keeps using the pins it replaced.
#
# The defaults come from the generator, so the images named in the run's own
# evidence are the ones it deployed. Keeping a second copy here is how the
# banner ended up reporting a version nothing was running.
OPERATOR_IMAGE="${OPERATOR_IMAGE:-quay.io/sbatsche/grid-operator:geo-d45b2d0}"
EPP_IMAGE="${EPP_IMAGE:-ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.10.0}"
GATEWAY_IMAGE="${GATEWAY_IMAGE:-quay.io/sbatsche/grid-ai-rollup:load-ab64bd8}"
export OPERATOR_IMAGE EPP_IMAGE GATEWAY_IMAGE
GRID_REPO="${GRID_REPO}" "${HERE}/generate.sh"

# Run from the grid checkout. xtask looks for praxis-forge at
# target/debug/praxis-forge relative to the working directory, so launching from
# anywhere else finds nothing and stops before the first cluster.
cd "${GRID_REPO}"

# kind runs rootful here, so this needs sudo, and sudo asks on the terminal.
# Run from a tool or an editor and there is none, so it fails at once and set -e
# ends the run, which reads as a script that did nothing.
if [ ! -t 0 ] && ! sudo -n true 2>/dev/null; then
  echo "This needs sudo and has no terminal to ask on. Run it from a shell." >&2
  exit 1
fi

# Clusters already attached to the wrong network stay attached: forge skips
# creating a cluster that exists, so a network fix cannot reach one that was
# built before it. FRESH=1 removes them so kind rebuilds them on the shared
# network.
if [ -n "${FRESH:-}" ]; then
  for c in pool-a pool-b pool-c; do
    sudo -E env PATH="$PATH" CONTAINER_HOST=unix:///run/podman/podman.sock \
      KIND_EXPERIMENTAL_PROVIDER=podman \
      kind delete cluster --name "grid-llmd-pm-${c}" 2>&1 | tail -1
  done
fi

exec sudo -E env \
  PATH="${GRID_REPO}/target/debug:$PATH" \
  HOME="$HOME" \
  CONTAINER_HOST=unix:///run/podman/podman.sock \
  KIND_EXPERIMENTAL_PROVIDER=podman \
  GRID_XTASK_OPERATOR_IMAGE="${OPERATOR_IMAGE}" \
  GRID_XTASK_EPP_IMAGE="${EPP_IMAGE}" \
  "${GRID_REPO}/target/debug/xtask" env run-grid-llmd-pool-metrics-demo \
    --forge-config "${HERE}/.generated/forge.yaml" \
    "$@"
