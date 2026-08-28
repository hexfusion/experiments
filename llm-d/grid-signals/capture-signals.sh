#!/usr/bin/env bash
# Record what the gateway would have read, as it would have read it.
#
# Writes one JSON object per scrape: the raw body the signals endpoint
# returned and the wall clock when it was fetched. Replaying that tape through
# the gateway's own parser and store reproduces its input exactly, including
# the operator's embedded timestamps, which is what makes a peer's sample
# arrive older than a local one.
#
# A time series database is the wrong shape for this. It records its own
# scrape time and would flatten the skew that tuning depends on. Use the
# Prometheus stack in observe.sh for exploring; use this for replay.
#
#   ./capture-signals.sh                       # 2 minutes at 4Hz from pool-a
#   SECONDS_TO_RUN=600 HZ=10 SITE=pool-b ./capture-signals.sh
#
# The output is a fixture for the replay harness:
#   praxis-proxy/ai .../filters/tests/fixtures/signals-trace.jsonl
set -euo pipefail

SITE="${SITE:-pool-a}"
HZ="${HZ:-4}"
SECONDS_TO_RUN="${SECONDS_TO_RUN:-120}"
OUT="${OUT:-signals-trace.jsonl}"
NS=grid-system
CTX="kind-grid-llmd-pm-${SITE}"
POD="${POD:-signals-reader}"

# Every signal the gateway scores on. Naming them is what makes the operator
# republish under the llm_d_epp_* names the scorers read.
COLLECT="collect=llm_d_epp_average_queue_size&collect=llm_d_epp_average_kv_cache_utilization"

kubectl --context "$CTX" -n "$NS" get pod "$POD" >/dev/null 2>&1 || {
  echo "no $POD in $SITE; the demo creates it, so run this while one is up" >&2
  exit 1
}

count=$(( SECONDS_TO_RUN * HZ ))
delay=$(awk -v hz="$HZ" 'BEGIN{printf "%.3f", 1/hz}')
echo "capturing ${count} scrapes from ${SITE} at ${HZ}Hz into ${OUT}" >&2

# The loop runs inside the pod so the interval is not the round trip of a
# kubectl exec per sample, which would be slower than the signal changes.
kubectl --context "$CTX" -n "$NS" exec "$POD" -- sh -c "
  i=0
  while [ \$i -lt ${count} ]; do
    printf '@%s\n' \"\$(date +%s%3N)\"
    curl -s --max-time 2 \
      --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt \
      'https://grid-operator-signals:9091/metrics?${COLLECT}' | grep '^llm_d_epp' || true
    printf '@@\n'
    sleep ${delay}
    i=\$((i+1))
  done" 2>/dev/null | python3 -c '
import json, sys
out, at, body = [], None, []
for line in sys.stdin:
    line = line.rstrip("\n")
    if line.startswith("@@"):
        if at is not None and body:
            out.append({"at_ms": at, "body": "\n".join(body) + "\n"})
        at, body = None, []
    elif line.startswith("@"):
        at = int(line[1:])
    elif line:
        body.append(line)
with open("'"$OUT"'", "w") as fh:
    for r in out:
        fh.write(json.dumps(r) + "\n")
print(f"wrote {len(out)} scrapes", file=sys.stderr)
if out:
    span = (out[-1]["at_ms"] - out[0]["at_ms"]) / 1000
    print(f"  spanning {span:.1f}s, {len(out[0][chr(34)+chr(34)] if False else out[0]["body"].splitlines())} series per scrape", file=sys.stderr)
'
