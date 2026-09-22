#!/usr/bin/env bash
# Tear down and recreate the AI-grid demo control plane, WITHOUT disturbing:
#   - the namespaces (so the mTLS/CA secrets in them survive)  [SECRETS.md]
#   - the site tenants (vLLM backends)
#   - the host-side cloudflared tunnel + maas-site-a-gateway    [helpers/TUNNEL.md]
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
CTX="${KCTX:-default/api-dagobah-hexfusion-local:6443/kube:admin}"

echo "==> teardown (demo objects only; namespaces + secrets preserved)"
# delete everything the package builds EXCEPT Namespace objects
kubectl kustomize "$HERE" \
  | awk 'BEGIN{RS="\n---\n"} !/^kind: Namespace$/ && !/\nkind: Namespace\n/' \
  | kubectl --context "$CTX" delete -f - --ignore-not-found --wait=false || true

echo "==> recreate"
kubectl --context "$CTX" apply -k "$HERE"
echo "==> done. secrets, sites, and the cloudflared tunnel were left untouched."
