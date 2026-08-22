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
            # A climb, not a level. The ramp spans the load phase so the
            # queue is still rising when the partition lands.
            - {name: PEAK_RATE, value: "${PEAK_RATE:-24}"}
            - {name: RAMP, value: "${K6_RAMP:-150s}"}
            - {name: HOLD, value: "${K6_HOLD:-180s}"}
          volumeMounts: [{name: s, mountPath: /scripts}]
      volumes: [{name: s, configMap: {name: k6-script}}]
MANIFEST
}

if [ "${1:-}" != "--chart" ]; then
  mkdir -p "${HERE}/.generated"; : > "$PHASES"

  BASELINE=${BASELINE_SECS:-45}
  LOADED_SECS=${LOAD_SECS:-90}
  CUT_LEN=${CUT_SECS:-90}
  HEAL_LEN=${HEAL_SECS:-90}

  # k6 runs from the load phase through to the end, so the partition happens
  # while the pool is still busy. Stopping it at the end of its own phase left
  # the queue back at zero before anything was cut, which measured a partition
  # of an idle grid.
  LOAD_TOTAL=$((LOADED_SECS + CUT_LEN + HEAL_LEN))

  mark baseline;            sleep "$BASELINE"
  mark "load on ${LOADED}"; K6_RAMP="${LOADED_SECS}s" K6_HOLD="$((CUT_LEN + HEAL_LEN))s" \
                           start_load; sleep "$LOADED_SECS"
  mark "blackhole ${CUT}";  "${HERE}/chaos.sh" blackhole "$CUT" >/dev/null 2>&1; sleep "$CUT_LEN"
  mark "healed";            "${HERE}/chaos.sh" blackhole-clear "$CUT" >/dev/null 2>&1; sleep "$HEAL_LEN"
  mark end

  kubectl --context "$(ctx "$LOADED")" -n "$NS" delete job k6-load --ignore-not-found >/dev/null 2>&1
  echo "run complete"
fi

python3 "${HERE}/chart.py" "$PHASES"
