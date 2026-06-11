#!/usr/bin/env bash
# scrape-envoy-stats.sh — record the ext_proc cluster circuit-breaker stats from
# the Envoy admin interface while the k6 ramp runs. This is the TRUTH SIGNAL for
# Arm 1: the `overflow` reset reason in llm-d#1582 maps to these counters/gauges.
#
# Stat names + meanings are from envoyproxy/envoy docs cluster_stats.rst:
#   upstream_rq_pending_overflow  counter  requests failed by pending/requests CB (HTTP/2 case) — THE #1582 counter
#   upstream_rq_active_overflow   counter  rejected: max_requests exhausted on a ready conn
#   upstream_cx_overflow          counter  connection circuit breaker overflowed
#   upstream_cx_pool_overflow     counter  connection-pool circuit breaker overflowed
#   circuit_breakers.<prio>.rq_open / cx_open / rq_pending_open  gauge  1 = breaker at capacity
#   upstream_rq_active / upstream_cx_active  gauge  live load at the trip point
#   upstream_rq_total / upstream_rq_5xx      counter context
#
# Pass condition for "it's the circuit breaker, not a hard limit":
#   500s appear iff a *_overflow counter climbs AND the matching *_open gauge == 1.
# In processing-mode arm B (response_body_mode: NONE) none of these should move.
#
# Envoy admin is per-gateway and usually bound to localhost:19000 inside the
# proxy pod. Reach it first, e.g.:
#   Envoy Gateway:  kubectl -n <ns> port-forward <envoy-pod> 19000:19000
#   Istio:          kubectl -n <ns> port-forward <istio-proxy-pod> 15000:15000  (ADMIN_URL=:15000)
#   kgateway:       kubectl -n <ns> port-forward <gw-pod> 19000:19000
# then point ADMIN_URL at the forwarded port.
#
# Usage:
#   CLUSTER=<extproc-cluster-substr> ADMIN_URL=http://localhost:19000 \
#     scripts/scrape-envoy-stats.sh [seconds]
# Env:
#   ADMIN_URL  default http://localhost:19000
#   CLUSTER    substring matching the ext_proc cluster name in admin stats (required)
#   INTERVAL   sample period seconds, default 1
#   OUT        output CSV, default results/envoy-stats-<epoch>.csv
# Arg1 (optional): total duration seconds; omit to run until Ctrl-C.

set -euo pipefail

ADMIN_URL="${ADMIN_URL:-http://localhost:19000}"
CLUSTER="${CLUSTER:-}"
INTERVAL="${INTERVAL:-1}"
DURATION="${1:-0}"
TS_START="$(date +%s)"
OUT="${OUT:-results/envoy-stats-${TS_START}.csv}"

if [[ -z "$CLUSTER" ]]; then
  echo "ERROR: set CLUSTER=<ext_proc cluster name substring> (see: curl $ADMIN_URL/clusters | grep ext)" >&2
  exit 2
fi
mkdir -p "$(dirname "$OUT")"

# Pull the cluster's stat block once per tick. /stats?filter narrows the payload.
fetch() { curl -fsS "${ADMIN_URL}/stats?filter=cluster\.${CLUSTER}" 2>/dev/null || true; }

# Extract one metric: sum the value across any matching lines (priorities, etc).
# Envoy line format: "cluster.<name>.<metric>: <int>"
val() { # $1=block  $2=metric-suffix-regex
  awk -F': ' -v re="$2" '$1 ~ re {s+=$2} END{print s+0}' <<<"$1"
}

cols="ts_epoch,ts_iso,rq_pending_overflow,rq_active_overflow,cx_overflow,cx_pool_overflow,rq_open,cx_open,rq_pending_open,rq_active,cx_active,rq_total,rq_5xx"
echo "$cols" | tee "$OUT"

echo "scraping cluster~='$CLUSTER' from $ADMIN_URL every ${INTERVAL}s -> $OUT" >&2

while :; do
  now="$(date +%s)"; iso="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  blk="$(fetch)"
  if [[ -z "$blk" ]]; then
    row="$now,$iso,NA,NA,NA,NA,NA,NA,NA,NA,NA,NA,NA"
  else
    rq_pending_overflow="$(val "$blk" '\.upstream_rq_pending_overflow$')"
    rq_active_overflow="$(val "$blk" '\.upstream_rq_active_overflow$')"
    cx_overflow="$(val "$blk" '\.upstream_cx_overflow$')"
    cx_pool_overflow="$(val "$blk" '\.upstream_cx_pool_overflow$')"
    rq_open="$(val "$blk" 'circuit_breakers\..*\.rq_open$')"
    cx_open="$(val "$blk" 'circuit_breakers\..*\.cx_open$')"
    rq_pending_open="$(val "$blk" 'circuit_breakers\..*\.rq_pending_open$')"
    rq_active="$(val "$blk" '\.upstream_rq_active$')"
    cx_active="$(val "$blk" '\.upstream_cx_active$')"
    rq_total="$(val "$blk" '\.upstream_rq_total$')"
    rq_5xx="$(val "$blk" '\.upstream_rq_5xx$')"
    row="$now,$iso,$rq_pending_overflow,$rq_active_overflow,$cx_overflow,$cx_pool_overflow,$rq_open,$cx_open,$rq_pending_open,$rq_active,$cx_active,$rq_total,$rq_5xx"
  fi
  echo "$row" | tee -a "$OUT" >/dev/null
  # live one-liner so overflow is visible as it trips
  printf '\r%s rq_active=%s cx_active=%s pending_overflow=%s active_overflow=%s rq_open=%s   ' \
    "$iso" "${rq_active:-NA}" "${cx_active:-NA}" "${rq_pending_overflow:-NA}" "${rq_active_overflow:-NA}" "${rq_open:-NA}" >&2

  if [[ "$DURATION" -gt 0 && $(( now - TS_START )) -ge "$DURATION" ]]; then
    echo >&2; echo "done ($DURATION s) -> $OUT" >&2; break
  fi
  sleep "$INTERVAL"
done
