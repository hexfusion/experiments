#!/usr/bin/env bash
# Render the package (or one step of it) with cluster values substituted for {tokens}.
#   ./helpers/render.sh [values.<env>.env] [target-dir]   # prints manifests
#   ./helpers/render.sh values.dagobah.env | kubectl apply -f -
#   ./helpers/render.sh values.dagobah.env steps/00-foundation | oc apply -f -
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
VALUES="${1:-$HERE/values.dagobah.env}"
TARGET="${2:-$HERE}"
[[ "$TARGET" != /* ]] && TARGET="$HERE/$TARGET"   # resolve relative to package root
# LoadRestrictionsNone lets a step overlay reference shared files above it (../..).
out="$(kubectl kustomize --load-restrictor LoadRestrictionsNone "$TARGET")"
while IFS='=' read -r k v; do
  [[ -z "$k" || "$k" == \#* ]] && continue
  out="${out//\{$k\}/$v}"
done < "$VALUES"
# fail only if one of OUR tokens (a values key) is left unresolved
miss=""
while IFS='=' read -r k v; do [[ -z "$k" || "$k" == \#* ]] && continue
  grep -q "{$k}" <<<"$out" && miss="$miss {$k}"; done < "$VALUES"
[[ -n "$miss" ]] && { echo "ERROR: unresolved:$miss (add to $VALUES)" >&2; exit 1; }
printf '%s\n' "$out"
