#!/usr/bin/env bash
# Run the grid signals demo on two kind clusters.
#
# Everything here is the shipped code: the operator image is built from the
# grid branch and carries operator::signals, the endpoint pickers are real
# llm-d, and the model servers are vllm-vcr. The only thing this directory
# adds is the image pin and the observability stack.
#
#   GRID_REPO=~/projects/praxis-proxy/grid ./run.sh
#
# Environment:
#   GRID_REPO       path to a praxis-proxy/grid checkout on the signals branch
#   OPERATOR_IMAGE  operator image to pin (default: the quay build below)
#   KEEP            set to keep the clusters up after the run
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${GRID_REPO:?set GRID_REPO to a praxis-proxy/grid checkout}"
GRID_REPO="$(cd "${GRID_REPO}" && pwd)"
OPERATOR_IMAGE="${OPERATOR_IMAGE:-quay.io/sbatsche/grid-operator:signals-4e01575}"
# The v0.8 endpoint picker was withdrawn when the package was renamed, so the
# topology's pin now returns 403. Upstream moved to the renamed repository;
# this pins the same thing until the branch catches up with it.
EPP_IMAGE="${EPP_IMAGE:-ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.9.0}"

TOPOLOGY="${GRID_REPO}/tests/e2e/topologies/grid-llmd-pool-metrics"
[ -f "${TOPOLOGY}/forge.yaml" ] || { echo "no topology at ${TOPOLOGY}" >&2; exit 1; }

# The upstream topology pins the released operator, which predates the signals
# module. Rather than fork 500 lines that are otherwise identical, rewrite the
# two image fields into a generated copy so the rest stays in step with the
# repo it came from.
GEN="${HERE}/.generated"
mkdir -p "${GEN}"
repo="${OPERATOR_IMAGE%:*}"
tag="${OPERATOR_IMAGE##*:}"
epp_repo="${EPP_IMAGE%:*}"
epp_tag="${EPP_IMAGE##*:}"
sed -E \
  -e "s|^([[:space:]]*operatorImage:).*|\1 \"${OPERATOR_IMAGE}\"|" \
  -e "s|^([[:space:]]*operatorImageRepo:).*|\1 \"${repo}\"|" \
  -e "s|^([[:space:]]*operatorImageTag:).*|\1 \"${tag}\"|" \
  -e "s|^([[:space:]]*eppImage:).*|\1 \"${EPP_IMAGE}\"|" \
  -e "s|^([[:space:]]*eppImageRepo:).*|\1 \"${epp_repo}\"|" \
  -e "s|^([[:space:]]*eppImageTag:).*|\1 \"${epp_tag}\"|" \
  "${TOPOLOGY}/forge.yaml" > "${GEN}/forge.yaml"

# The config names its manifests by paths relative to itself, so the generated
# copy needs the same neighbours. Linking rather than copying keeps one source
# of truth for everything except the two lines that were rewritten.
for dir in resources configs; do
  [ -e "${TOPOLOGY}/${dir}" ] || continue
  ln -sfn "${TOPOLOGY}/${dir}" "${GEN}/${dir}"
done

echo "operator image: ${OPERATOR_IMAGE}"
grep -c "${repo}" "${GEN}/forge.yaml" | xargs -I{} echo "pinned in {} places"

teardown=(--teardown)
[ -n "${KEEP:-}" ] && teardown=()

cd "${GRID_REPO}"
exec cargo xtask env run-grid-llmd-pool-metrics-demo \
  --forge-config "${GEN}/forge.yaml" \
  "${teardown[@]}" \
  "$@"
