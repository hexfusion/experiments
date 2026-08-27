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
#
# Every pin has to reach xtask as its own GRID_XTASK_ variable. Rewriting the
# topology is not enough: xtask reads these rather than the file, so a pin that
# is only rewritten there is reported by the banner and never deployed. The
# gateway went a whole run that way, on a release image predating the load
# collector the run was meant to exercise.
#
# Never, because these clusters cannot resolve a registry: the nodes inherit a
# search list that breaks the lookup, so anything not loaded in never arrives.
# shellcheck source=pins.sh
. "${HERE}/pins.sh"
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

# The run talks to the rootful podman socket, and images are built against the
# rootless one. They are separate stores, so a freshly built pin is invisible
# here and xtask stops with "absent; build it", which reads as a missing build
# rather than a missing copy. Copy anything the rootful store does not have.
for img in "${OPERATOR_IMAGE}" "${EPP_IMAGE}" "${GATEWAY_IMAGE}" "${KEYCLOAK_IMAGE}"; do
  if sudo podman image exists "${img}"; then
    continue
  fi
  if podman image exists "${img}"; then
    echo "staging ${img} into the rootful store"
    tar="$(mktemp -t grid-img-XXXXXX.tar)"
    podman save "${img}" -o "${tar}"
    sudo podman load -i "${tar}"
    rm -f "${tar}"
  elif [ "${img}" = "${KEYCLOAK_IMAGE}" ]; then
    # The only pin nothing here builds, so it is the one worth fetching.
    echo "pulling ${img}"
    podman pull "${img}"
    tar="$(mktemp -t grid-img-XXXXXX.tar)"
    podman save "${img}" -o "${tar}"
    sudo podman load -i "${tar}"
    rm -f "${tar}"
  else
    echo "warning: ${img} is in neither store; the run will stop on it" >&2
  fi
done

# Not exec, so a successful run can publish what it just proved.
run_demo() {
sudo -E env \
  PATH="${GRID_REPO}/target/debug:$PATH" \
  HOME="$HOME" \
  CONTAINER_HOST=unix:///run/podman/podman.sock \
  KIND_EXPERIMENTAL_PROVIDER=podman \
  GRID_XTASK_OPERATOR_IMAGE="${OPERATOR_IMAGE}" \
  GRID_XTASK_EPP_IMAGE="${EPP_IMAGE}" \
  GRID_XTASK_GATEWAY_IMAGE="${GATEWAY_IMAGE}" \
  GRID_XTASK_IMAGE_PULL_POLICY="${GRID_XTASK_IMAGE_PULL_POLICY:-Never}" \
  "${GRID_REPO}/target/debug/xtask" env run-grid-llmd-pool-metrics-demo \
    --forge-config "${HERE}/.generated/forge.yaml" \
    "$@"
}

run_demo "$@"
status=$?

# Publishes the pins this run just exercised, and only when it passed.
# Set PUSH=0 to keep a run local.
# Pushing on failure puts an image on the registry that nothing vouches for,
# which is how a tag that was never proven ends up deployed somewhere.
if [ "${PUSH:-1}" != "0" ] && [ "${status}" -eq 0 ]; then
  for img in "${OPERATOR_IMAGE}" "${GATEWAY_IMAGE}"; do
    case "${img}" in
      quay.io/*)
        echo "pushing ${img}"
        podman push --authfile "${HOME}/.config/containers/auth.json" \
          "quay.io:443/${img#quay.io/}" || echo "warning: push failed for ${img}" >&2
        ;;
      *) echo "skipping ${img}: not a quay pin" ;;
    esac
  done
elif [ "${PUSH:-1}" != "0" ]; then
  echo "not pushing: the run exited ${status}" >&2
fi

exit "${status}"
