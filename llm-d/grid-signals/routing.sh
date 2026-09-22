#!/usr/bin/env bash
# Show the gateway routing on live load rather than on the order it was handed.
#
# The same three signals the hub and spoke demo used: geography at rest,
# capacity under skew, affinity across both.
#
#   ./routing.sh run
#   ./routing.sh clean
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=grid-system
ENTRY="${ENTRY:-pool-a}"      # gateway receiving measurement traffic
SKEWED="${SKEWED:-pool-a}"    # pool whose queue is driven up directly
OUT="${HERE}/.generated"
PHASES="${OUT}/routing-phases"
ctx() { echo "kind-grid-llmd-pm-$1"; }

# shellcheck source=load.sh
. "${HERE}/load.sh"

REST="${REST:-90}"
SKEW="${SKEW:-150}"
HEAL="${HEAL:-120}"
STICKY="${STICKY:-150}"

mark() {
  local ms; ms=$(date +%s%3N)
  printf '%s\t%s\n' "$ms" "$1" >> "$PHASES"
  kubectl --context "$(ctx "$ENTRY")" -n "$NS" exec deploy/prometheus -- \
    wget -qO- --timeout=30 --header='Content-Type: application/json' \
    --post-data="{\"time\":${ms},\"tags\":[\"routing\"],\"text\":\"$1\"}" \
    "http://grafana.${NS}.svc:3000/api/annotations" >/dev/null 2>&1 || true
  echo "$(date -u +%H:%M:%S)  $1"
}

queues() {
  for site in pool-a pool-b pool-c; do
    q=$(kubectl --context "$(ctx "$site")" get --raw \
      "/api/v1/namespaces/${NS}/services/llmd-epp-metrics:9090/proxy/metrics" 2>/dev/null \
      | awk '/^llm_d_epp_average_queue_size/ {print $2; exit}')
    printf '      %-8s queue=%s\n' "$site" "${q:-n/a}"
  done
}

case "${1:-run}" in
  run)
    mkdir -p "$OUT"; : > "$PHASES"
    START=$(date +%s%3N)
    GW="http://consumer-gateway.${NS}.svc:8080"
    # Straight at the pool's inference service. Skew driven through the gateway
    # would let the router avoid the condition the run is creating.
    VCR="http://vcr-service.${NS}.svc:8000"
    TOTAL=$((REST + SKEW + HEAL))

    mark "rest"
    load_job "$ENTRY" k6-measure "$GW" 6 "$TOTAL" 0
    sleep "$REST"; queues

    mark "skew ${SKEWED}"
    load_job "$SKEWED" k6-skew "$VCR" 40 "$SKEW" 0
    sleep "$SKEW"; queues

    mark "heal"
    load_stop "$SKEWED" k6-skew
    sleep "$HEAL"; queues

    # Sessions bind to a cluster, so a fixed pool of ids measures a binding
    # where unique ids per request would measure nothing.
    mark "sticky under skew"
    load_stop "$ENTRY" k6-measure
    load_job "$ENTRY" k6-measure "$GW" 6 "$STICKY" 20
    load_job "$SKEWED" k6-skew "$VCR" 40 "$STICKY" 0
    sleep "$STICKY"; queues

    mark "end"
    load_stop "$ENTRY" k6-measure; load_stop "$SKEWED" k6-skew
    echo "$START $(date +%s%3N)" > "${OUT}/routing-window"
    echo "window: $(cat "${OUT}/routing-window")"
    ;;
  clean)
    for s in pool-a pool-b pool-c; do load_stop "$s" k6-measure; load_stop "$s" k6-skew; done
    echo cleaned
    ;;
  *) sed -n '2,8p' "$0"; exit 2 ;;
esac
