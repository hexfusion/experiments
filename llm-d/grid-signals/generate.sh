#!/usr/bin/env bash
# Rewrite the grid topology's image pins into a generated copy.
#
# Sourced by both runners so neither can use a config the other left behind.
# The two runners diverged once already: one regenerated and the other did not,
# so a topology fix landed in the repo and the run kept using the old pins.
set -euo pipefail

: "${GRID_REPO:?set GRID_REPO to a praxis-proxy/grid checkout}"
GEN_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOPOLOGY="${GRID_REPO}/tests/e2e/topologies/grid-llmd-pool-metrics"
[ -f "${TOPOLOGY}/forge.yaml" ] || { echo "no topology at ${TOPOLOGY}" >&2; exit 1; }

# shellcheck source=pins.sh
. "${GEN_HERE}/pins.sh"

GEN="${GEN_HERE}/.generated"
mkdir -p "${GEN}"
sed -E \
  -e "s|^([[:space:]]*operatorImage:).*|\1 \"${OPERATOR_IMAGE}\"|" \
  -e "s|^([[:space:]]*operatorImageRepo:).*|\1 \"${OPERATOR_IMAGE%:*}\"|" \
  -e "s|^([[:space:]]*operatorImageTag:).*|\1 \"${OPERATOR_IMAGE##*:}\"|" \
  -e "s|^([[:space:]]*eppImage:).*|\1 \"${EPP_IMAGE}\"|" \
  -e "s|^([[:space:]]*eppImageRepo:).*|\1 \"${EPP_IMAGE%:*}\"|" \
  -e "s|^([[:space:]]*eppImageTag:).*|\1 \"${EPP_IMAGE##*:}\"|" \
  -e "s|^([[:space:]]*gatewayImage:).*|\1 \"${GATEWAY_IMAGE}\"|" \
  -e "s|^([[:space:]]*gatewayImageRepo:).*|\1 \"${GATEWAY_IMAGE%:*}\"|" \
  -e "s|^([[:space:]]*gatewayImageTag:).*|\1 \"${GATEWAY_IMAGE##*:}\"|" \
  "${TOPOLOGY}/forge.yaml" > "${GEN}/forge.yaml"

# The config names its manifests relative to itself, so the generated copy needs
# the same neighbours. Links rather than copies, so a fix to the topology's
# manifests is picked up without regenerating anything.
for dir in resources configs; do
  [ -e "${TOPOLOGY}/${dir}" ] && ln -sfn "${TOPOLOGY}/${dir}" "${GEN}/${dir}"
done

# Every pin, because a banner that lists some of them reads as a full account
# of what the run deploys. The gateway went unlisted while it was the one pin
# that mattered.
echo "operator: ${OPERATOR_IMAGE}"
echo "gateway:  ${GATEWAY_IMAGE}"
echo "epp:      ${EPP_IMAGE}"
echo "keycloak: ${KEYCLOAK_IMAGE}"
