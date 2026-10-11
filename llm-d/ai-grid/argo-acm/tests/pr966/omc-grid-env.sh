#!/usr/bin/env bash
# Put the GRID environment on the RHOAI model controller. Run again after any ODH operator
# restart: the kserve module recreates the Deployment and 3.5.1 ignores managed=false.
set -euo pipefail
kubectl set env deployment/odh-model-controller -n redhat-ods-applications -c manager \
  GRID_NAMESPACE_SELECTOR='{"matchLabels":{"grid.praxis.fast/allowed":"true"}}' \
  GRID_NETWORK_NAME=grid \
  GRID_NAMESPACE=grid \
  GRID_SERVICE_ACCOUNT=grid-model-client \
  GRID_TOKEN_SECRET=grid-model-client-token \
  GRID_SITE_LABELS=app.kubernetes.io/instance=grid-hq
