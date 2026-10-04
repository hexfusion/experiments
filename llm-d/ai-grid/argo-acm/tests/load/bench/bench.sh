#!/usr/bin/env bash
# Load the grid front door, or one site's vLLM directly, with guidellm from a Job on the hub, at
# explicit constant rates under a concurrency cap so the auth path stays inside its capacity.
#   ./bench.sh front long    front door, about 512 prompt and 256 output tokens
#   ./bench.sh direct short  site-a's vLLM, about 128 and 64
#   ./bench.sh cleanup       delete the grid-load namespace
# The MaaS key is read from KEY_FILE into a Secret and never printed. watch.py enforces the
# guardrails and deletes the run on a vLLM or maas-api restart, a provider Ready=False, or a node low on memory.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
kubeconfig=${GRID_KUBECONFIG:-$HOME/.kube/grid.kubeconfig}
k() { kubectl --kubeconfig "$kubeconfig" --context dagobah "$@"; }

if [ "${1:-}" = cleanup ]; then
  k delete namespace grid-load --wait=false
  exit 0
fi
kind=${1:?usage: bench.sh front|direct long|short, or cleanup}
shape=${2:?usage: bench.sh front|direct long|short}
case $shape in
  long) export PROMPT_TOKENS=${PROMPT_TOKENS:-512} OUTPUT_TOKENS=${OUTPUT_TOKENS:-256} ;;
  short) export PROMPT_TOKENS=${PROMPT_TOKENS:-128} OUTPUT_TOKENS=${OUTPUT_TOKENS:-64} ;;
  *) echo "unknown shape $shape" >&2; exit 2 ;;
esac
# Dataset shape, see gen_data.py: a shared system prompt and multi-turn conversations.
export PREFIX_TOKENS=${PREFIX_TOKENS:-0} TURNS=${TURNS:-0} CONVS=${CONVS:-64} COUNT=${COUNT:-8000}
case $kind in
  front) export TARGET="https://${FRONT_DOOR_HOST:-maas.acme.lab}" API_KEY_ARG=',api_key=$(MAAS_KEY)' ;;
  direct) export TARGET=${DIRECT_TARGET:-https://qwen3-kserve-workload-svc.ai-tenant-site-a.svc.cluster.local:8000} API_KEY_ARG='' ;;
  *) echo "unknown target $kind" >&2; exit 2 ;;
esac
export FRONT_DOOR_HOST=${FRONT_DOOR_HOST:-maas.acme.lab}
export ROUTER_IP=${ROUTER_IP:-192.168.1.201}
export MODEL=${MODEL:-qwen3-coder-30b-a3b}
# RATES (req/s, comma separated) each run for STEP_SECONDS, never above MAX_CONCURRENCY in flight.
export RATES=${RATES:-0.5,1,2,4,8,16} MAX_CONCURRENCY=${MAX_CONCURRENCY:-64} STEP_SECONDS=${STEP_SECONDS:-75}
# PROFILE overrides the rate steps, for example {"kind":"concurrent","streams":[64,128,256]}.
export PROFILE=${PROFILE:-"{\"kind\":\"constant\",\"rate\":[$RATES],\"max_concurrency\":$MAX_CONCURRENCY}"}
export RUN="gridbench-$(date +%Y%m%d-%H%M%S)-${LABEL:-$kind-$shape}" NAME
NAME=$RUN
out=${OUT:-$HOME/.local/share/grid-bench}/$RUN
mkdir -p "$out"
key_file=${KEY_FILE:-$HOME/.config/grid-bench-maas-key}

k create namespace grid-load --dry-run=client -o yaml | k apply -f - >/dev/null
keytmp=$(mktemp)
trap 'rm -f "$keytmp"' EXIT
tr -d '\r\n' < "$key_file" > "$keytmp"
k -n grid-load create secret generic grid-load-key --from-file=key="$keytmp" --dry-run=client -o yaml | k apply -f - >/dev/null
rm -f "$keytmp"
k -n grid-load create configmap grid-bench --from-file="$here/gen_data.py" --from-file="$here/warmup.py" \
  --dry-run=client -o yaml | k apply -f - >/dev/null
# One warm request first, so the api-key cache is warm; COLD=1 measures the cold-cache case instead.
export WARMUP="python /load/warmup.py $TARGET $MODEL"
[ "${COLD:-0}" = 1 ] && WARMUP=true

# Per-site counters once a second for the whole run, for attribution (no served-by header).
python3 "$here/sites_sampler.py" "$out/sites.jsonl" &
sampler=$!
trap 'rm -f "$keytmp"; kill $sampler 2>/dev/null' EXIT
date +%s > "$out/start"
envsubst '${NAME} ${RUN} ${ROUTER_IP} ${FRONT_DOOR_HOST} ${TARGET} ${MODEL} ${API_KEY_ARG} ${PROMPT_TOKENS} ${OUTPUT_TOKENS} ${PROFILE} ${STEP_SECONDS} ${WARMUP} ${PREFIX_TOKENS} ${TURNS} ${CONVS} ${COUNT}' \
  < "$here/job.yaml.tmpl" | k apply -f - >/dev/null
echo "started $RUN: rates $RATES req/s, ${STEP_SECONDS}s each, at most $MAX_CONCURRENCY in flight, against $TARGET; results in $out"

# The watcher ends when guidellm prints its done marker, or exits 3 after deleting the run.
watch_rc=0
python3 "$here/watch.py" --run "$RUN" --kubeconfig "$kubeconfig" > "$out/watch.csv" || watch_rc=$?
if [ "$watch_rc" -ne 0 ]; then
  echo "watcher stopped the run (exit $watch_rc); see $out/watch.csv" >&2
  exit "$watch_rc"
fi
date +%s > "$out/end"
kill $sampler 2>/dev/null || true
pod=$(k -n grid-load get pods -l "job-name=$RUN" -o jsonpath='{.items[0].metadata.name}')
k -n grid-load logs "$pod" > "$out/guidellm.log"
k -n grid-load exec "$pod" -- cat /tmp/out.json > "$out/guidellm.json"
k -n grid-load delete job "$RUN" --wait=false >/dev/null
python3 "$here/summarize.py" "$out/guidellm.json" "$kind-$shape" | tee "$out/summary.md"
python3 "$here/sites_report.py" "$out" | tee -a "$out/summary.md"
echo "raw report: $out/guidellm.json"
