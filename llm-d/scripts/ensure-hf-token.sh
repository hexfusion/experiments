#!/bin/bash
# Ensure a `hf-token` Secret exists in the target namespace, sourced from
# ~/.config/hf-token. Reusable across all experiments under experiments/llm-d/.
#
# Usage:
#   ensure-hf-token.sh <namespace>
#
# Exit codes:
#   0 — secret present (already existed or freshly created)
#   1 — token file missing or unreadable
#   2 — kubectl error

set -euo pipefail

NS="${1:?usage: $0 <namespace>}"
TOKEN_FILE="${HF_TOKEN_FILE:-$HOME/.config/hf-token}"
SECRET_NAME="hf-token"

# If the secret already exists, nothing to do.
if kubectl -n "$NS" get secret "$SECRET_NAME" >/dev/null 2>&1; then
  echo "secret/$SECRET_NAME exists in $NS"
  exit 0
fi

# Source token from $TOKEN_FILE.
if [ ! -r "$TOKEN_FILE" ]; then
  cat >&2 <<EOF
ERROR: $TOKEN_FILE is missing or unreadable.

Create it with:
  echo -n 'hf_xxx_your_token' > $TOKEN_FILE
  chmod 600 $TOKEN_FILE

Or override the path:
  HF_TOKEN_FILE=/path/to/your/token $0 $NS
EOF
  exit 1
fi

# Ensure namespace exists (idempotent).
kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"

# Create the secret.
kubectl -n "$NS" create secret generic "$SECRET_NAME" \
  --from-literal=HF_TOKEN="$(cat "$TOKEN_FILE")"

echo "Created secret/$SECRET_NAME in $NS from $TOKEN_FILE"
