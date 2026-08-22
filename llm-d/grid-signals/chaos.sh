#!/usr/bin/env bash
# Break the signals path on purpose, without killing anything.
#
# The grid e2e suite already has verify-failover-under-lost-peer, and it says
# in its own doc comment that the partition is "simulated by process kill, not
# real network-level isolation". That difference matters here. A killed
# operator stops answering and stops polling, so its peer sees a member that
# left. A reachable-but-unanswering one keeps running, keeps serving what it
# already holds, and keeps ageing, which is the state the staleness signals
# exist to report.
#
# Withdrawing one TCP port from the SWIM Service stages exactly that: SWIM is
# UDP, so membership still says the site is alive, while TCP 9091 stops
# resolving anywhere.
#
#   ./chaos.sh cut pool-b        # pool-b's signals become unreachable
#   ./chaos.sh heal pool-b       # and reachable again
#   ./chaos.sh status            # what each site can see and reach
set -euo pipefail

NS=grid-system
ctx() { echo "kind-grid-llmd-pm-$1"; }
SWIM_PORT='{"name":"swim-udp","port":7946,"targetPort":"swim-udp","protocol":"UDP"}'
SIG_PORT='{"name":"signals","port":9091,"targetPort":"signals","protocol":"TCP"}'

patch_ports() {
  kubectl --context "$(ctx "$1")" -n "$NS" patch svc grid-operator-swim \
    --type=merge -p "{\"spec\":{\"ports\":[$2]}}"
}

proxy() {
  kubectl --context "$(ctx "$1")" get --raw \
    "/api/v1/namespaces/${NS}/services/$2:$3/proxy/metrics" 2>/dev/null || true
}

case "${1:-}" in
  cut)
    patch_ports "${2:?site}" "$SWIM_PORT"
    echo "cut: ${2} signals unreachable, SWIM left alone"
    ;;
  heal)
    patch_ports "${2:?site}" "$SWIM_PORT,$SIG_PORT"
    echo "healed: ${2} signals reachable again"
    ;;
  status)
    printf '%-10s %-26s %s\n' SITE 'HOLDS SIGNALS FOR' 'REACHES'
    for site in pool-a pool-b; do
      sites=$(proxy "$site" grid-operator-signals 9091 \
        | grep -o 'grid_site="[a-z-]*"' | sort -u | sed 's/.*="//;s/"//' | tr '\n' ' ')
      up=$(proxy "$site" grid-operator-metrics 9090 \
        | awk '/^grid_collection_up/ && $NF==1 {print $0}' \
        | grep -o 'peer="[a-z-]*"' | sed 's/.*="//;s/"//' | tr '\n' ' ')
      printf '%-10s %-26s %s\n' "$site" "${sites:-none}" "${up:-none}"
    done
    ;;
  *)
    sed -n '2,18p' "$0"
    exit 2
    ;;
esac
