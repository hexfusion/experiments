#!/usr/bin/env bash
# Snapshot the live AI-grid demo resources to a local fallback before a
# teardown + from-scratch redeploy. Read-only against the cluster.
#
#   ./helpers/backup-live.sh [context]
#
# Writes to ./backup/<UTC-timestamp>/ (gitignored: it contains Secrets). The
# critical content is the Secrets (mTLS/CA + provider keys) that are NOT in the
# repo, plus any live-only or hand-patched objects the manifests do not carry.
#
# Restore path if the from-scratch deploy fails:
#   1. Re-apply the Secrets from the backup:  oc apply -f backup/<ts>/<ns>/secrets.yaml
#   2. Re-apply the repo manifests:           kubectl apply -k .
#   3. Diff live vs backup to catch drift the repo does not carry.
set -euo pipefail

CTX="${1:-}"
OC=(oc)
[[ -n "$CTX" ]] && OC=(oc --context "$CTX")

HERE="$(cd "$(dirname "$0")/.." && pwd)"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$HERE/backup/$STAMP"
mkdir -p "$OUT"

# Demo namespaces (from the manifests).
NAMESPACES=(
  grid-system ai-grid-demo ai-grid-v3-scratch grid-tokens grid-grafana
  ai-tenant-site-a ai-tenant-site-b ai-tenant-site-d vllm praxis-raw
)

# Namespaced kinds to capture. Standard workload/config/network kinds plus the
# grid/MaaS/inference CRs the demo installs.
KINDS="deploy,statefulset,daemonset,job,cronjob,svc,route,configmap,secret,pvc,serviceaccount,role,rolebinding,poddisruptionbudget"
CR_KINDS="limitador,inferenceprovider,inferenceobjective,endpointpickerconfig,maasauthpolicy,maasmodelref,maassubscription,gateway,envoyfilter,podmonitor,imagestream"

echo "backup -> $OUT"
for ns in "${NAMESPACES[@]}"; do
  "${OC[@]}" get namespace "$ns" >/dev/null 2>&1 || { echo "  skip $ns (absent)"; continue; }
  mkdir -p "$OUT/$ns"
  echo "  $ns"
  # Secrets alone (the restore-critical, non-repo state), kept separate.
  "${OC[@]}" get secret -n "$ns" -o yaml > "$OUT/$ns/secrets.yaml" 2>/dev/null || true
  # Everything else, one kind at a time so an unknown kind cannot wipe the dump.
  : > "$OUT/$ns/resources.yaml"
  for kind in ${KINDS//,/ } ${CR_KINDS//,/ }; do
    out="$("${OC[@]}" get "$kind" -n "$ns" -o yaml 2>/dev/null)" || continue
    printf '%s\n' "$out" | grep -q '^items: \[\]$' && continue
    printf -- '---\n# %s\n%s\n' "$kind" "$out" >> "$OUT/$ns/resources.yaml"
  done
done

# Cluster-scoped grid CRs (GridSite/GridNetwork are cluster-scoped).
mkdir -p "$OUT/_cluster"
"${OC[@]}" get gridsite,gridnetwork -o yaml > "$OUT/_cluster/grid.yaml" 2>/dev/null || true

echo "done. Secrets are in <ns>/secrets.yaml; full state in <ns>/resources.yaml."
echo "This directory is gitignored (contains Secrets). Keep it off the repo."
