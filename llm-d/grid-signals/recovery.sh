#!/usr/bin/env bash
# Partition, heal, cold start: what the poll clients report through each.
#
# Three questions, in one run so they share a timeline:
#
#   1. Does a partition show up as retries on the poll clients, rather than
#      just as a gap in the data?
#   2. Once the partition lifts, how fast does the site recover?
#   3. If an operator restarts with nothing in its store, how fast does it
#      refill?
#
# Two and three are different recoveries and it is worth not conflating them.
# Lifting a partition leaves both sides holding what they had, so recovery is
# one successful poll. A cold start throws the store away, so the site has to
# re-answer from an empty map and its peers have to re-poll it. The second is
# the slower one and it is the one that bounds a rollout.
#
# Phase boundaries are posted to Grafana as annotations, so the rendered
# panels carry the timeline rather than needing a caption to be read.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=grid-system
CTX=kind-grid-llmd-pm-pool-a
VICTIM="${VICTIM:-pool-a}"
OUT="${HERE}/.generated"
mkdir -p "$OUT/shots"

# Baseline first: a panel that starts at the partition cannot show what
# normal looked like, and "it recovered" only means something against that.
BASELINE="${BASELINE:-90}"
CUT="${CUT:-150}"
HEAL="${HEAL:-150}"
COLD="${COLD:-180}"

# Traffic runs the whole time, offered at a site that is not the victim.
# A partition with nothing flowing proves the poll clients notice; a partition
# under load also shows what the router was holding when it went stale.
# shellcheck source=load.sh
. "${HERE}/load.sh"
LOADED="${LOADED:-pool-b}"

gf() {
  kubectl --context "$CTX" -n "$NS" exec deploy/prometheus -- \
    wget -qO- --timeout=30 --header='Content-Type: application/json' \
    --post-data="$1" "http://grafana.${NS}.svc:3000/api/annotations" 2>/dev/null || true
}

# Recorded as well as posted. Grafana keeps annotations in a SQLite file on an
# emptyDir, so any restart of it loses them and a re-render of an old window
# comes back with no phase markers at all. The file is what makes a capture
# repeatable; ./recovery.sh annotate replays it.
mark() {
  local text="$1" ms
  ms=$(date +%s%3N)
  printf '%s\t%s\n' "$ms" "$text" >> "$PHASES"
  gf "{\"time\":${ms},\"tags\":[\"recovery\"],\"text\":\"${text}\"}" >/dev/null
  echo "$(date -u +%H:%M:%S)  ${text}"
}

# Re-post a recorded run's phases, so old windows render with their markers.
replay() {
  [ -f "$PHASES" ] || { echo "no phases recorded at ${PHASES}" >&2; exit 1; }
  while IFS=$'\t' read -r ms text; do
    [ -n "$ms" ] || continue
    gf "{\"time\":${ms},\"tags\":[\"recovery\"],\"text\":\"${text}\"}" >/dev/null
    echo "replayed ${text}"
  done < "$PHASES"
}

PHASES="${OUT}/recovery-phases"

if [ "${1:-}" = "annotate" ]; then
  replay
  exit 0
fi

: > "$PHASES"
START=$(date +%s%3N)
echo "== recovery run, victim=${VICTIM}, load at ${LOADED}"

TOTAL=$((BASELINE + CUT + HEAL + COLD))
K6_RAMP=60s K6_HOLD="$((TOTAL - 60))s" load_start "$LOADED"

mark "baseline"
sleep "$BASELINE"

mark "partition ${VICTIM}"
"${HERE}/chaos.sh" blackhole "$VICTIM" >/dev/null
sleep "$CUT"

mark "heal"
"${HERE}/chaos.sh" blackhole-clear "$VICTIM" >/dev/null
sleep "$HEAL"

mark "operator cold start ${VICTIM}"
kubectl --context "kind-grid-llmd-pm-${VICTIM}" -n "$NS" \
  delete pod -l app.kubernetes.io/name=grid-operator --wait=false >/dev/null 2>&1 || true
sleep "$COLD"

mark "end"
END=$(date +%s%3N)
load_stop "$LOADED"

echo "$START $END" > "$OUT/recovery-window"
echo
echo "window: ${START} .. ${END}"
echo "capture with: FROM=${START} TO=${END} ./shots.sh <panelId> <name>"
