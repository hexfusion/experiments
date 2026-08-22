#!/usr/bin/env bash
# Deploy a Prometheus into each cluster, scraping what production would.
#
# Per cluster rather than one reaching across, because that is the shape a real
# deployment has: Prometheus scrapes the services beside it over ClusterIP. One
# central collector pulling operator internals over the grid's own LoadBalancer
# would measure a topology nobody runs.
#
#   ./observe.sh up        # deploy, and print each site's Prometheus address
#   ./observe.sh urls      # print them again
#   ./observe.sh down
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=grid-system
SITES=(pool-a pool-b pool-c)
ctx() { echo "kind-grid-llmd-pm-$1"; }

case "${1:-up}" in
  up)
    for site in "${SITES[@]}"; do
      kubectl --context "$(ctx "$site")" get ns "$NS" >/dev/null 2>&1 || { echo "skip $site (no cluster)"; continue; }
      kubectl --context "$(ctx "$site")" -n "$NS" create configmap prometheus-rules \
        --from-file="${HERE}/observability/alerts.yml" \
        --dry-run=client -o yaml | kubectl --context "$(ctx "$site")" apply -f - >/dev/null
      sed "s/SITE_NAME/${site}/g" "${HERE}/observability/prometheus-incluster.yaml" \
        | kubectl --context "$(ctx "$site")" apply -f - >/dev/null
      echo "deployed prometheus to ${site}"
    done
    "$0" urls
    ;;
  urls)
    for site in "${SITES[@]}"; do
      ip=$(kubectl --context "$(ctx "$site")" -n "$NS" get svc prometheus \
        -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
      printf '  %-8s http://%s:9090\n' "$site" "${ip:-<pending>}"
    done
    ;;
  down)
    for site in "${SITES[@]}"; do
      kubectl --context "$(ctx "$site")" -n "$NS" delete deploy/prometheus svc/prometheus \
        cm/prometheus-config cm/prometheus-rules --ignore-not-found >/dev/null 2>&1 || true
    done
    echo "removed"
    ;;
  *) sed -n '2,12p' "$0"; exit 2 ;;
esac
