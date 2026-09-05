#!/usr/bin/env bash
# Assert the cluster prerequisites this demo CONSUMES. It installs none of them.
#
# The platform operators (RHOAI/KServe, the Gateway API + inference extension,
# and the MaaS control plane) are shared cluster infra, owned by whoever runs
# the cluster -- not by this package. This demo deliberately does NOT roll out
# operators (no Subscription, CatalogSource, OperatorGroup, DSC, and nothing
# from Kuadrant). It only hand-rolls its own CRDs (grid.praxis-proxy.io) and
# its own workloads. This script checks the platform pieces are present and
# fails fast with a plain message if one is missing, so a missing operator
# surfaces here instead of as a confusing apply error later.
#
# Usage: helpers/preflight.sh
#   KUBECTL=oc CONTEXT=my-ctx helpers/preflight.sh   # overrides
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
CTX_ARG=()
[ -n "${CONTEXT:-}" ] && CTX_ARG=(--context "$CONTEXT")

fail=0
have_crd() {
  # $1 = CRD name, $2 = human label (what provides it)
  if "$KUBECTL" "${CTX_ARG[@]}" get crd "$1" >/dev/null 2>&1; then
    printf '  PASS  %-52s (%s)\n' "$1" "$2"
  else
    printf '  FAIL  %-52s (%s)\n' "$1" "$2"
    fail=1
  fi
}

echo "Cluster prerequisites (this package installs NONE of these -- it asserts them):"
echo
# Gateway API + the inference extension (GIE) -- routing plane.
have_crd gateways.gateway.networking.k8s.io                 "Gateway API"
have_crd gatewayclasses.gateway.networking.k8s.io           "Gateway API"
have_crd inferenceobjectives.inference.networking.x-k8s.io  "gateway-api-inference-extension"
# KServe / RHOAI -- the model serving CRD the vllm/ sites use.
have_crd llminferenceservices.serving.kserve.io             "KServe (RHOAI)"
# MaaS control plane -- ships bundled in RHOAI 3.5, not a separate operator.
have_crd maassubscriptions.maas.opendatahub.io             "MaaS / RHOAI 3.5"
have_crd maasauthpolicies.maas.opendatahub.io              "MaaS / RHOAI 3.5"
# OpenShift Route -- enrollment/ and grid/ expose over Routes. Route is an
# aggregated API (not a CRD), so check the API surface, not the CRD list.
if "$KUBECTL" "${CTX_ARG[@]}" api-resources --api-group=route.openshift.io 2>/dev/null | grep -qw routes; then
  printf '  PASS  %-52s (%s)\n' "routes.route.openshift.io" "OpenShift"
else
  printf '  FAIL  %-52s (%s)\n' "routes.route.openshift.io" "OpenShift"
  fail=1
fi

echo
# At least one GatewayClass must exist for the maas/ gateways to bind to.
if [ "$("$KUBECTL" "${CTX_ARG[@]}" get gatewayclass -o name 2>/dev/null | wc -l)" -gt 0 ]; then
  echo "  PASS  a GatewayClass exists for the maas/ gateways to bind to"
else
  echo "  FAIL  no GatewayClass found -- the maas/ gateways have nothing to bind to"
  fail=1
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "PREFLIGHT FAILED. Install the missing platform operator(s) first; this"
  echo "package does not install them (see README 'Cluster prerequisites')."
  exit 1
fi
echo "Preflight OK. The demo's own CRDs (grid.praxis-proxy.io) are applied by the"
echo "package itself -- no need to pre-install those."
