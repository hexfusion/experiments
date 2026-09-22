#!/usr/bin/env bash
# Convenience wrapper for the AI Grid debug bastion (helpers/bastion.yaml).
#
#   ./helpers/bastion.sh up                 # create the bastion
#   ./helpers/bastion.sh curl <url> [args]  # curl from inside the cluster
#   ./helpers/bastion.sh epp <site>         # scrape a site's EPP /metrics over mTLS
#   ./helpers/bastion.sh sh                  # interactive shell in the bastion
#   ./helpers/bastion.sh down               # delete the bastion
#
# Pass a cluster with CONTEXT=... (defaults to the current kube context).
set -euo pipefail
NS=grid-system
DEP=deploy/grid-bastion
CTX=${CONTEXT:-}
ocx() { if [ -n "$CTX" ]; then oc --context "$CTX" "$@"; else oc "$@"; fi; }
here=$(cd "$(dirname "$0")" && pwd)

case "${1:-}" in
  up)
    ocx apply -f "$here/bastion.yaml"
    ocx -n "$NS" rollout status "$DEP" --timeout=90s
    ;;
  down)
    ocx delete -f "$here/bastion.yaml" --ignore-not-found
    ;;
  sh)
    ocx -n "$NS" exec -it "$DEP" -- /bin/bash
    ;;
  curl)
    shift
    ocx -n "$NS" exec "$DEP" -- curl -s "$@"
    ;;
  epp)
    site=${2:?usage: bastion.sh epp <site>   e.g. site-a}
    url="https://qwen3-epp-service.ai-tenant-${site}.svc:9090/metrics"
    ocx -n "$NS" exec "$DEP" -- curl -s \
      --cacert /certs/ca/ca.crt --cert /certs/client/tls.crt --key /certs/client/tls.key \
      "$url"
    ;;
  *)
    grep -E '^#( |$)' "$0" | sed 's/^# \{0,1\}//'
    exit 1
    ;;
esac
