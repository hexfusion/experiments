#!/usr/bin/env bash
# Offer load at a site, through the consumer gateway.
#
#   ./load.sh start pool-b        # ramp then hold, as set by the env below
#   ./load.sh stop pool-b
#
# Sourced by the scenario scripts as well as runnable on its own, so the job
# spec lives in one place and a change to it cannot apply to one scenario and
# not the other.
set -euo pipefail
LOAD_NS="${LOAD_NS:-grid-system}"
LOAD_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
load_ctx() { echo "kind-grid-llmd-pm-$1"; }

load_stop() {
  kubectl --context "$(load_ctx "$1")" -n "$LOAD_NS" \
    delete job k6-load --ignore-not-found >/dev/null 2>&1 || true
}

load_start() {
  local site="$1"
  load_stop "$site"
  kubectl --context "$(load_ctx "$site")" -n "$LOAD_NS" create configmap k6-script \
    --from-file="${LOAD_HERE}/load/k6.js" --dry-run=client -o yaml \
    | kubectl --context "$(load_ctx "$site")" apply -f - >/dev/null
  kubectl --context "$(load_ctx "$site")" -n "$LOAD_NS" apply -f - >/dev/null <<MANIFEST
apiVersion: batch/v1
kind: Job
metadata: {name: k6-load, namespace: ${LOAD_NS}}
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: k6
          image: ${K6_IMAGE:-docker.io/grafana/k6:0.55.0}
          command: ["k6", "run", "/scripts/k6.js"]
          env:
            # The consumer gateway, which is where traffic actually enters.
            # It routes across the grid on the signals under test, so the queue
            # appears wherever the router sent it rather than where the load was
            # offered. That is the behaviour being measured, not noise in it.
            #
            # LOAD_TARGET can point at vcr-service to drive one pool directly
            # and take the router out of the picture, which isolates a single
            # site's held-versus-true gap at the cost of testing less.
            #
            # Never the endpoint picker: it speaks gRPC ext-proc and has no
            # HTTP inference endpoint.
            - {name: TARGET, value: "${LOAD_TARGET:-http://consumer-gateway.${LOAD_NS}.svc:8080}"}
            - {name: PEAK_RATE, value: "${PEAK_RATE:-24}"}
            - {name: RAMP, value: "${K6_RAMP:-150s}"}
            - {name: HOLD, value: "${K6_HOLD:-180s}"}
          volumeMounts: [{name: s, mountPath: /scripts}]
      volumes: [{name: s, configMap: {name: k6-script}}]
MANIFEST
}

# Only act when run directly, so sourcing defines the functions and nothing else.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  case "${1:-}" in
    start) load_start "${2:?site}" ;;
    stop)  load_stop  "${2:?site}" ;;
    *) sed -n '2,9p' "$0"; exit 2 ;;
  esac
fi
