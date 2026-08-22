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
    # Grafana lands in the first site and holds one datasource per Prometheus.
    # The addresses are only known after MetalLB assigns them, so the
    # datasource file is built here rather than shipped.
    home="${SITES[0]}"
    ds=""
    for site in "${SITES[@]}"; do
      ip=""
      for _ in $(seq 1 30); do
        ip=$(kubectl --context "$(ctx "$site")" -n "$NS" get svc prometheus \
          -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
        [ -n "$ip" ] && break
        sleep 2
      done
      [ -z "$ip" ] && { echo "no address for ${site} prometheus yet" >&2; continue; }
      ds="${ds}      - name: ${site}
        type: prometheus
        uid: ${site}
        access: proxy
        url: http://${ip}:9090
        isDefault: $([ "$site" = "$home" ] && echo true || echo false)
"
    done

    kubectl --context "$(ctx "$home")" -n "$NS" create configmap grafana-dashboards \
      --from-file="${HERE}/observability/grafana/dashboards/grid.json" \
      --dry-run=client -o yaml | kubectl --context "$(ctx "$home")" apply -f - >/dev/null

    awk -v ds="$ds" '{gsub(/^DATASOURCES$/, ds); print}' "${HERE}/observability/grafana.yaml" \
      | kubectl --context "$(ctx "$home")" apply -f - >/dev/null
    kubectl --context "$(ctx "$home")" -n "$NS" rollout status deploy/grafana --timeout=90s >/dev/null 2>&1 || true
    echo "grafana deployed to ${home}"

    "$0" urls
    ;;
  grafana)
    # port-forward, because the LoadBalancer range is on the container network
    # and the host does not route to it.
    echo "grafana on http://localhost:3000  (ctrl-c to stop)"
    exec kubectl --context "$(ctx "${SITES[0]}")" -n "$NS" port-forward svc/grafana 3000:3000
    ;;
  urls)
    echo "  grafana:  ./observe.sh grafana   then http://localhost:3000"
    for site in "${SITES[@]}"; do
      ip=$(kubectl --context "$(ctx "$site")" -n "$NS" get svc prometheus \
        -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
      printf '  %-8s http://%s:9090\n' "$site" "${ip:-<pending>}"
    done
    ;;
  down)
    for site in "${SITES[@]}"; do
      kubectl --context "$(ctx "$site")" -n "$NS" delete deploy/prometheus svc/prometheus \
        deploy/grafana svc/grafana cm/prometheus-config cm/prometheus-rules \
        cm/grafana-datasources cm/grafana-dashboards cm/grafana-dashboards-provider \
        --ignore-not-found >/dev/null 2>&1 || true
    done
    echo "removed"
    ;;
  *) sed -n '2,12p' "$0"; exit 2 ;;
esac
