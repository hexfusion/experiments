#!/usr/bin/env bash
# Run a timed sequence of failures and record when each one started.
#
# The phases are what makes the graphs readable: without boundaries a queue
# that rises and a queue that rose because somebody pushed it look the same.
# Each phase writes its start time, and the chart draws them as bands.
#
#   ./scenario.sh            # run it
#   ./scenario.sh --chart    # chart the last run without re-running
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=grid-system
LOADED=${LOADED:-pool-b}      # the site that gets the traffic
CUT=${CUT:-pool-a}            # the site that loses its peers
PHASES="${HERE}/.generated/phases.json"
ctx() { echo "kind-grid-llmd-pm-$1"; }

mark() { printf '{"phase":"%s","at":%s}\n' "$1" "$(date +%s)" >> "$PHASES"; echo "-- $1"; }

start_load() {
  kubectl --context "$(ctx "$LOADED")" -n "$NS" delete job k6-load --ignore-not-found >/dev/null 2>&1
  kubectl --context "$(ctx "$LOADED")" -n "$NS" create configmap k6-script \
    --from-file="${HERE}/load/k6.js" --dry-run=client -o yaml \
    | kubectl --context "$(ctx "$LOADED")" apply -f - >/dev/null
  kubectl --context "$(ctx "$LOADED")" -n "$NS" apply -f - >/dev/null <<MANIFEST
apiVersion: batch/v1
kind: Job
metadata: {name: k6-load, namespace: ${NS}}
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
            - {name: TARGET, value: "http://consumer-gateway.${NS}.svc:8080"}
            - {name: RATE, value: "${RATE:-40}"}
            - {name: DURATION, value: "${LOAD_SECS:-180}s"}
          volumeMounts: [{name: s, mountPath: /scripts}]
      volumes: [{name: s, configMap: {name: k6-script}}]
MANIFEST
}

if [ "${1:-}" != "--chart" ]; then
  mkdir -p "${HERE}/.generated"; : > "$PHASES"

  mark baseline;            sleep "${BASELINE_SECS:-60}"
  mark "load on ${LOADED}"; start_load; sleep "${LOAD_SECS:-180}"
  mark "blackhole ${CUT}";  "${HERE}/chaos.sh" blackhole "$CUT" >/dev/null 2>&1; sleep "${CUT_SECS:-90}"
  mark "healed";            "${HERE}/chaos.sh" blackhole-clear "$CUT" >/dev/null 2>&1; sleep "${HEAL_SECS:-90}"
  mark end

  kubectl --context "$(ctx "$LOADED")" -n "$NS" delete job k6-load --ignore-not-found >/dev/null 2>&1
  echo "run complete"
fi

python3 "${HERE}/chart.py" "$PHASES"
