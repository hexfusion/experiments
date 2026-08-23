#!/usr/bin/env bash
# Capture dashboard panels as images over a given window.
#
# Rendered by Grafana itself rather than redrawn, so what lands in a writeup is
# what the dashboard shows. Grafana answers /render with an apology image when
# no renderer is installed, and that apology is a valid PNG, so the size check
# below is the difference between a capture and a picture of a warning.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=grid-system
CTX=kind-grid-llmd-pm-pool-a
DASH=grid-signals-full/grid-signals
OUT="${HERE}/.generated/shots"
FROM="${FROM:?set FROM as an epoch millisecond}"
TO="${TO:?set TO as an epoch millisecond}"
SITE="${SITE:-pool-a}"

mkdir -p "$OUT"
shot() {
  local id="$1" name="$2" h="${3:-380}"
  kubectl --context "$CTX" -n "$NS" exec deploy/prometheus -- \
    wget -qO- --timeout=60 \
    "http://grafana.${NS}.svc:3000/render/d-solo/${DASH}?panelId=${id}&var-site=${SITE}&from=${FROM}&to=${TO}&width=1100&height=${h}&theme=light" \
    > "${OUT}/${name}.png" 2>/dev/null
  local size; size=$(stat -c '%s' "${OUT}/${name}.png")
  if [ "$size" -lt 12000 ]; then
    echo "  ${name}: ${size} bytes, too small to be a panel" >&2
  else
    echo "  ${name}: ${size} bytes"
  fi
}

shot "${1:-5}" "${2:-panel}" "${3:-380}"
