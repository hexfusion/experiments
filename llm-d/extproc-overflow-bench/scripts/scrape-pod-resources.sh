#!/usr/bin/env bash
# scrape-pod-resources.sh — at the concurrency knee, NAME the binding resource.
# The capacity formula (README) says the held-path max is one of:
#   A1  ext_proc breaker overflow   -> seen in scrape-envoy-stats.sh (*_overflow)
#   A2  Envoy CPU / FD / pod memory -> seen HERE (gateway pod saturates)
#   B   downstream (sim) / Envoy conn throughput -> backend pod or Envoy conns HERE
# Run this alongside scrape-envoy-stats.sh during every max-concurrency run so the
# ceiling can be attributed, not guessed.
#
# Captures per tick: gateway pod CPU/mem (+ limits), FD count, Envoy worker count
# and live connections, backend pod CPU/mem. CPU at/near the limit = CPU-bound;
# FD near ulimit = FD-bound; overflow with low CPU = breaker-bound (the A1 case).
#
# Usage:
#   NS=<ns> GW_SELECTOR=<label> BACKEND_SELECTOR=<label> \
#     ADMIN_URL=http://localhost:19000 scripts/scrape-pod-resources.sh [seconds]
# Env:
#   NS                kube namespace (required)
#   GW_SELECTOR       label selector for the Envoy/gateway pod(s) (required), e.g. "gateway.envoyproxy.io/owning-gateway-name=eg"
#   BACKEND_SELECTOR  label selector for the sim/vLLM backend pods (optional)
#   ADMIN_URL         Envoy admin (optional) for server.concurrency + total_connections
#   INTERVAL          seconds, default 2
#   OUT               CSV, default results/pod-resources-<epoch>.csv
# Arg1 (optional): duration seconds; omit to run until Ctrl-C.

set -euo pipefail

NS="${NS:?set NS=<namespace>}"
GW_SELECTOR="${GW_SELECTOR:?set GW_SELECTOR=<label selector for gateway pods>}"
BACKEND_SELECTOR="${BACKEND_SELECTOR:-}"
ADMIN_URL="${ADMIN_URL:-}"
INTERVAL="${INTERVAL:-2}"
DURATION="${1:-0}"
TS_START="$(date +%s)"
OUT="${OUT:-results/pod-resources-${TS_START}.csv}"
mkdir -p "$(dirname "$OUT")"

# top of a selector -> "sumCPUm sumMemMi" across matching pods
top_sum() { # $1=selector
  kubectl -n "$NS" top pod -l "$1" --no-headers 2>/dev/null \
    | awk '{c+=($2+0); g=$3; sub(/[A-Za-z]+$/,"",g); m+=g} END{printf "%d %d", c+0, m+0}'
}
# first matching pod name
pod1() { kubectl -n "$NS" get pod -l "$1" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null; }
# cpu/mem limit (millicores / Mi) of first gw pod's first container
gw_limits() {
  local p; p="$(pod1 "$GW_SELECTOR")"; [[ -z "$p" ]] && { echo "0 0"; return; }
  kubectl -n "$NS" get pod "$p" -o jsonpath='{.spec.containers[0].resources.limits.cpu}{" "}{.spec.containers[0].resources.limits.memory}' 2>/dev/null \
    | awk '{c=$1; if(c ~ /m$/){sub(/m$/,"",c)} else {c=c*1000} g=$2; sub(/[A-Za-z]+$/,"",g); printf "%s %s", c+0, g+0}'
}
# open fd count inside the first gw pod (proc 1)
gw_fds() {
  local p; p="$(pod1 "$GW_SELECTOR")"; [[ -z "$p" ]] && { echo "NA"; return; }
  kubectl -n "$NS" exec "$p" -- sh -c 'ls /proc/1/fd 2>/dev/null | wc -l' 2>/dev/null || echo "NA"
}
envoy_stat() { # $1=metric substring
  [[ -z "$ADMIN_URL" ]] && { echo "NA"; return; }
  curl -fsS "${ADMIN_URL}/stats?filter=$1" 2>/dev/null | awk -F': ' 'NR==1{print $2+0; found=1} END{if(!found)print "NA"}'
}

read -r GW_CPU_LIM GW_MEM_LIM < <(gw_limits)

cols="ts_epoch,ts_iso,gw_cpu_m,gw_cpu_limit_m,gw_mem_mi,gw_mem_limit_mi,gw_fds,envoy_workers,envoy_total_conns,backend_cpu_m,backend_mem_mi"
echo "$cols" | tee "$OUT"
echo "scraping NS=$NS gw='$GW_SELECTOR' (cpu_limit=${GW_CPU_LIM}m mem_limit=${GW_MEM_LIM}Mi) every ${INTERVAL}s -> $OUT" >&2

while :; do
  now="$(date +%s)"; iso="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  read -r GW_CPU GW_MEM < <(top_sum "$GW_SELECTOR")
  FDS="$(gw_fds)"
  WORKERS="$(envoy_stat 'server.concurrency')"
  CONNS="$(envoy_stat 'server.total_connections')"
  if [[ -n "$BACKEND_SELECTOR" ]]; then read -r BK_CPU BK_MEM < <(top_sum "$BACKEND_SELECTOR"); else BK_CPU=NA; BK_MEM=NA; fi
  row="$now,$iso,${GW_CPU:-0},${GW_CPU_LIM},${GW_MEM:-0},${GW_MEM_LIM},${FDS},${WORKERS},${CONNS},${BK_CPU},${BK_MEM}"
  echo "$row" | tee -a "$OUT" >/dev/null

  # live one-liner: CPU% of limit is the tell for A2 (CPU-bound) vs A1 (breaker-bound)
  cpu_pct="NA"; [[ "${GW_CPU_LIM:-0}" -gt 0 && -n "${GW_CPU:-}" ]] && cpu_pct=$(( GW_CPU * 100 / GW_CPU_LIM ))
  printf '\r%s gw_cpu=%sm(%s%% of lim) gw_mem=%sMi fds=%s conns=%s   ' \
    "$iso" "${GW_CPU:-0}" "$cpu_pct" "${GW_MEM:-0}" "${FDS}" "${CONNS}" >&2

  if [[ "$DURATION" -gt 0 && $(( now - TS_START )) -ge "$DURATION" ]]; then
    echo >&2; echo "done ($DURATION s) -> $OUT" >&2; break
  fi
  sleep "$INTERVAL"
done
