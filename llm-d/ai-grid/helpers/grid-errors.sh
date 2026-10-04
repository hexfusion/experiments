#!/usr/bin/env bash
# Watch error and panic log lines across every grid site, from one terminal.
#
# Reads the hq LokiStack's application tenant, which collects the grid,
# grid-enrollment, ai-tenant-*, redhat-ai-gateway-infra and llm-d namespaces
# from hq and from every site. A line matches when its parsed level is error or
# critical, OR when it contains a Rust panic ("panicked at"), a Go panic
# ("panic:" / "goroutine "), a vLLM traceback ("Traceback") or an OOM marker --
# so panics show even when the level parse missed them.
#
# Read-only. It port-forwards the hq Loki gateway and queries it with your oc
# token; it writes nothing. Needs oc (logged in to hq), curl and jq.
#
# Usage:
#   helpers/grid-errors.sh [--since 1h] [--site hq|factory|retail|all] [--limit 200]
#   helpers/grid-errors.sh --follow [--site ...]     # tail new lines every 10s
#
# Env overrides: OC=oc, NS=openshift-logging, LOCAL_PORT=18089, TOKEN=<bearer>
set -euo pipefail

OC="${OC:-oc}"
NS="${NS:-openshift-logging}"
LOCAL_PORT="${LOCAL_PORT:-18089}"
SINCE="1h"
SITE="all"
LIMIT="200"
FOLLOW=0

while [ $# -gt 0 ]; do
  case "$1" in
    --since) SINCE="$2"; shift 2 ;;
    --site)  SITE="$2";  shift 2 ;;
    --limit) LIMIT="$2"; shift 2 ;;
    --follow) FOLLOW=1; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for bin in "$OC" curl jq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "need $bin on PATH" >&2; exit 1; }
done

# --since to seconds. Accepts a bare number (seconds) or a suffix s/m/h/d.
to_seconds() {
  local v="$1" n unit
  n="${v%[smhd]}"; unit="${v#"$n"}"
  case "$unit" in
    s|"") echo "$n" ;;
    m) echo $((n * 60)) ;;
    h) echo $((n * 3600)) ;;
    d) echo $((n * 86400)) ;;
    *) echo "bad --since: $v (use 30m, 2h, 1d)" >&2; exit 2 ;;
  esac
}

# Stream selector, scoped to a site when asked.
selector='{log_type="application"'
if [ "$SITE" != "all" ]; then
  selector="${selector}, openshift_labels_cluster=\"${SITE}\""
fi
selector="${selector}}"
QUERY="${selector} | regexp \"(?P<panic_hit>panicked at|panic:|goroutine |Traceback|CUDA out of memory|OutOfMemoryError|OOMKilled|out of memory)\" | panic_hit!=\"\" or level=~\"error|critical\""

TOKEN="${TOKEN:-$("$OC" whoami -t)}"
[ -n "$TOKEN" ] || { echo "no oc token; run: oc login" >&2; exit 1; }

# Port-forward the hq Loki gateway for the life of the script.
"$OC" -n "$NS" port-forward svc/logging-loki-gateway-http "${LOCAL_PORT}:8080" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill "$PF_PID" 2>/dev/null || true' EXIT
BASE="https://127.0.0.1:${LOCAL_PORT}/api/logs/v1/application/loki/api/v1/query_range"

# Wait for the forward to accept connections.
for _ in $(seq 1 30); do
  curl -sk -o /dev/null --max-time 2 "https://127.0.0.1:${LOCAL_PORT}/" && break
  sleep 0.5
done

# query <start_ns> <end_ns> -> tab-separated: ts_ns, site, pod, line (one per entry).
# On an HTTP error or a non-success Loki status, print why to stderr and return 1,
# so a broken query never looks the same as "no errors".
query() {
  local resp http body status
  resp=$(curl -sk --max-time 15 -w $'\n%{http_code}' -G "$BASE" \
    -H "Authorization: Bearer ${TOKEN}" \
    --data-urlencode "query=${QUERY}" \
    --data-urlencode "start=$1" \
    --data-urlencode "end=$2" \
    --data-urlencode "limit=${LIMIT}" \
    --data-urlencode "direction=backward") \
    || { echo "grid-errors: curl to Loki failed (timeout or no connection)" >&2; return 1; }
  http=${resp##*$'\n'}
  body=${resp%$'\n'*}
  if [ "$http" != "200" ]; then
    echo "grid-errors: Loki returned HTTP $http: $(printf '%s' "$body" | head -c 300)" >&2
    return 1
  fi
  status=$(printf '%s' "$body" | jq -r '.status // "unknown"')
  if [ "$status" != "success" ]; then
    echo "grid-errors: Loki status=$status: $(printf '%s' "$body" | jq -r '.error // .message // "no detail"')" >&2
    return 1
  fi
  printf '%s' "$body" | jq -r '.data.result[]? as $r | $r.values[]? |
      [ .[0],
        ($r.stream.openshift_labels_cluster // "?"),
        ($r.stream.kubernetes_pod_name // "?"),
        (.[1] | gsub("[\t\n]"; " ")) ] | @tsv' \
  | sort -n
}

now_ns() { echo "$(date -u +%s)000000000"; }

print_rows() {
  # drop the ts column; print site, pod, line (line already tab/newline-free)
  awk -F'\t' '{ printf "%-8s %-46s %s\n", $2, $3, $4 }'
}

if [ "$FOLLOW" -eq 0 ]; then
  start=$(( $(date -u +%s) - $(to_seconds "$SINCE") ))000000000
  out=$(query "$start" "$(now_ns)") || exit 1
  [ -n "$out" ] && printf '%s\n' "$out" | print_rows
  exit 0
fi

# Follow: seed at now, then every 10s print only lines newer than the last seen.
echo "watching ${SITE} for errors and panics (ctrl-c to stop)" >&2
last=$(now_ns)
while true; do
  sleep 10
  end=$(now_ns)
  rows=$(query "$((last + 1))" "$end" || true)
  if [ -n "$rows" ]; then
    echo "$rows" | print_rows
    last=$(echo "$rows" | tail -1 | cut -f1)
  else
    last=$end
  fi
done
