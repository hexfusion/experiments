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

# The job spec lives in load.sh so the scenarios cannot drift apart.
# shellcheck source=load.sh
. "${HERE}/load.sh"
start_load() { load_start "$LOADED"; }

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

  load_stop "$LOADED"
  echo "run complete"
fi

python3 "${HERE}/chart.py" "$PHASES"
