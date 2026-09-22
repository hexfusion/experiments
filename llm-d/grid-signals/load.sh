#!/usr/bin/env bash
# Offer load at a site. One job spec, so the scenarios cannot drift apart.
#
#   ./load.sh start pool-b                          # through the gateway
#   ./load.sh job pool-a skew http://x:8000 40 120 0
#   ./load.sh stop pool-b [name]
set -euo pipefail
LOAD_NS="${LOAD_NS:-grid-system}"
LOAD_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
load_ctx() { echo "kind-grid-llmd-pm-$1"; }

load_stop() {
  kubectl --context "$(load_ctx "$1")" -n "$LOAD_NS" \
    delete job "${2:-k6-load}" --ignore-not-found >/dev/null 2>&1 || true
}

# site name target rate hold sessions
load_job() {
  local site="$1" name="$2" target="$3" rate="$4" hold="$5" sessions="${6:-0}" ramp="${7:-5s}"
  load_stop "$site" "$name"
  kubectl --context "$(load_ctx "$site")" -n "$LOAD_NS" create configmap k6-script \
    --from-file="${LOAD_HERE}/load/k6.js" --dry-run=client -o yaml \
    | kubectl --context "$(load_ctx "$site")" apply -f - >/dev/null
  kubectl --context "$(load_ctx "$site")" -n "$LOAD_NS" apply -f - >/dev/null <<MANIFEST
apiVersion: batch/v1
kind: Job
metadata: {name: ${name}, namespace: ${LOAD_NS}}
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
            - {name: TARGET, value: "${target}"}
            - {name: START_RATE, value: "${rate}"}
            - {name: PEAK_RATE, value: "${rate}"}
            - {name: RAMP, value: "${ramp}"}
            - {name: HOLD, value: "${hold}s"}
            - {name: SESSIONS, value: "${sessions}"}
          volumeMounts: [{name: s, mountPath: /scripts}]
      volumes: [{name: s, configMap: {name: k6-script}}]
MANIFEST
}

# The consumer gateway routes across the grid on the signals under test, so the
# queue appears where the router sent it rather than where load was offered.
load_start() {
  load_job "$1" k6-load "${LOAD_TARGET:-http://consumer-gateway.${LOAD_NS}.svc:8080}" \
    "${PEAK_RATE:-24}" "${K6_HOLD_SECS:-180}" 0 "${K6_RAMP:-150s}"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  case "${1:-}" in
    start) load_start "${2:?site}" ;;
    job)   load_job "${2:?site}" "${3:?name}" "${4:?target}" "${5:?rate}" "${6:?hold}" "${7:-0}" ;;
    stop)  load_stop "${2:?site}" "${3:-k6-load}" ;;
    *) sed -n '2,7p' "$0"; exit 2 ;;
  esac
fi
