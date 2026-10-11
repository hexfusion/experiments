#!/usr/bin/env bash
# Point the RHOAI operator CSV related image for odh-model-controller at an image.
# Usage: omc-csv-image.sh <image-ref>   (the original is in omc-original.image)
set -euo pipefail
IMG="${1:?image ref}"
CSV=rhods-operator.3.5.1
NS=redhat-ods-operator
IDX=$(kubectl get csv -n "$NS" "$CSV" -o json | jq -r '.spec.install.spec.deployments[0].spec.template.spec.containers[0].env | to_entries[] | select(.value.name=="RELATED_IMAGE_ODH_MODEL_CONTROLLER_IMAGE") | .key')
echo "env index $IDX -> $IMG"
kubectl patch csv -n "$NS" "$CSV" --type=json \
  -p "[{\"op\":\"replace\",\"path\":\"/spec/install/spec/deployments/0/spec/template/spec/containers/0/env/$IDX/value\",\"value\":\"$IMG\"}]"
