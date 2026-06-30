#!/usr/bin/env bash
# Tear down all three clusters.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../scripts" && pwd)/common.sh"
need kind
for c in "${CLUSTERS[@]}"; do
  kind get clusters 2>/dev/null | grep -qx "$c" && { log "deleting $c"; kind_run delete cluster --name "$c"; }
done
log "all clusters deleted"
