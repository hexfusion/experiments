#!/usr/bin/env bash
# One-shot bring-up for the single-extproc-pick experiment.
#
# Stack: MaaS control plane (maas-api + maas-controller) + MaaS UI drive one
# praxis gateway (policy = user auth + per-user token budget, epp_router = local
# llm-d pick) with Limitador as the shared counter, in front of a local llm-d
# instance. Benchmark it by scaling praxis-gateway replicas and driving many
# users with guidellm.
#
# Status: partial. Steps marked TODO need work called out in README.md before
# the stack stands up end to end.
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
NS=maas-scale
CTX="${CTX:-dagobah}"
oc() { command oc --context "$CTX" "$@"; }

echo ">> [1/6] namespace"
oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -

echo ">> [2/6] local llm-d instance (backend + InferencePool + EPP + epp-http-gateway)"
# From manifests/llmd-pick-reference (05-praxis-epp pattern), minus its own
# praxis proxy. Namespace-rewrite to $NS.
# TODO: apply the pool/EPP/backend/epp-http-gateway from llmd-pick-reference
#       (exclude 20-praxis.yaml, that is our gateway below), retargeted to $NS.
echo "   TODO: wire llmd-pick-reference (pool/EPP/backend) into $NS"

echo ">> [3/6] Limitador (durable shared counter) + the praxis gateway"
oc apply -k "$HERE"

echo ">> [4/6] MaaS control plane (maas-api + maas-controller)"
# From ~/projects/opendatahub-io/models-as-a-service; the dagobah deploy is the
# starting config. maas-controller must be pointed at this praxis chain so it
# reconciles a MaaSSubscription into policy.yaml + the Limitador limits.
echo "   TODO: apply maas-api + maas-controller (models-as-a-service), point the"
echo "         controller output at config/policy.yaml + manifests/limitador/limits.yaml"

echo ">> [5/6] MaaS UI (consumer portal)"
# From ~/projects/opendatahub-io/odh-dashboard (distributions/maas-consumer-portal).
echo "   TODO: deploy the MaaS consumer portal"

echo ">> [6/6] a user token to drive load"
# A MaaSSubscription (via the UI or maas-api) mints a per-user token with a
# token budget; the budget lands in Limitador as a per-sub max_value.
echo "   TODO: mint a user token + set its Limitador budget, then:"
echo "         guidellm ... --target http://<praxis-gateway>/v1 --api-key <token>"

echo ">> done (partial). See README.md for the deltas still to build:"
echo "   - the quota/limitador plugin (M1) and a praxis image that includes it"
echo "   - the maas-controller output pointed at this chain (M2/M3)"
