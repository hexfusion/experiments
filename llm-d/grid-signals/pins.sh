#!/usr/bin/env bash
# The images every runner here deploys.
#
# One file because the defaults lived in three and drifted: a runner was still
# naming an operator from before the signals module, so a run meant to exercise
# the polling path deployed code that had none of it. A pin is only correct
# against a tree, not against a date, so each is named for the commit whose
# sources it was built from.
#
# Override any of them per run:
#
#   OPERATOR_IMAGE=... ./run-rootful.sh
#
# Updating one means rebuilding, pushing, and naming the new tag here. Check a
# pin still matches its tree with:
#
#   git rev-parse HEAD:operator          # against the tag's commit
#
# grid, operator/: serves and polls signals over mTLS, and no longer
# gossips them or ranks the overlay by them.
OPERATOR_IMAGE="${OPERATOR_IMAGE:-quay.io/sbatsche/grid-operator:signals-47c807c}"

# ai: the consumer and provider gateways. Carries the policy filter, so the
# rate limiter has an authenticated identity to bucket on.
GATEWAY_IMAGE="${GATEWAY_IMAGE:-quay.io/sbatsche/grid-ai-rollup:auth-c48b3bb}"

# Upstream llm-d, not built here. The v0.8 picker was withdrawn when the
# package was renamed, which is why this names the renamed repository.
EPP_IMAGE="${EPP_IMAGE:-ghcr.io/llm-d/llm-d-router-endpoint-picker:v0.10.0}"

# The grid's issuer. Upstream, not built here.
KEYCLOAK_IMAGE="${KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0}"

export OPERATOR_IMAGE GATEWAY_IMAGE EPP_IMAGE KEYCLOAK_IMAGE
