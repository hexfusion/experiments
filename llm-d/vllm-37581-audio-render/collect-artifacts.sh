#!/bin/bash
# Capture a complete artifact snapshot of the current vllm-37581 experiment state.
# Output: artifacts/run-<timestamp>/ (gitignored). Self-contained for bug-report attachment.
#
# Usage:
#   ./collect-artifacts.sh                   # default output dir
#   ./collect-artifacts.sh /tmp/my-snapshot  # custom output dir
#
# Captures (per artifact dir):
#   meta.txt           — timestamp, kubectl context, image, vLLM version (best-effort)
#   pod-describe.txt   — kubectl describe pod
#   pod-yaml.yaml      — kubectl get pod -o yaml (includes status/conditions)
#   pod-logs.txt       — full kubectl logs (no --tail limit)
#   pod-logs-prev.txt  — previous container instance logs if any restarts
#   events.txt         — namespace events sorted by lastTimestamp
#   deployment.yaml    — Deployment as-applied
#   service.yaml       — Service as-applied
#   secret-summary.txt — Secret type/keys (not values)
#   request.json       — what we sent (the audio-payload.json fixture)
#   response.json      — last render response captured in cluster-stacktrace.log
#   stacktrace.txt     — TypeError lines extracted from pod logs (if any)

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
NS="vllm-37581"
OUT="${1:-$DIR/artifacts/run-$(date -u +%Y-%m-%dT%H-%M-%SZ)}"

mkdir -p "$OUT"
echo "== Collecting artifacts → $OUT =="

# Meta
{
  echo "timestamp_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "kubectl_context: $(kubectl config current-context 2>&1)"
  echo "namespace: $NS"
  echo "experiment_dir: $DIR"
  echo "git_remote: $(cd "$DIR" && git remote get-url origin 2>/dev/null || echo n/a)"
  echo "git_sha: $(cd "$DIR" && git rev-parse HEAD 2>/dev/null || echo n/a)"
  echo "image_ref: $(kubectl -n "$NS" get deploy vllm-qwen3-asr -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo n/a)"
} > "$OUT/meta.txt"

# Pod state
POD=$(kubectl -n "$NS" get pod -l app=vllm-qwen3-asr -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -n "$POD" ]; then
  kubectl -n "$NS" describe pod "$POD" > "$OUT/pod-describe.txt" 2>&1 || true
  kubectl -n "$NS" get pod "$POD" -o yaml > "$OUT/pod-yaml.yaml" 2>&1 || true
  kubectl -n "$NS" logs "$POD" > "$OUT/pod-logs.txt" 2>&1 || true
  kubectl -n "$NS" logs "$POD" --previous > "$OUT/pod-logs-prev.txt" 2>&1 || true

  # Extract vLLM version best-effort from startup banner
  grep -m1 -i "vllm.*version\|API server.*version\|vLLM API server v" "$OUT/pod-logs.txt" >> "$OUT/meta.txt" 2>/dev/null || true

  # Pull out the TypeError stack if present
  awk '/Traceback/,/^[A-Z][a-zA-Z]*Error:/' "$OUT/pod-logs.txt" > "$OUT/stacktrace.txt" 2>/dev/null || true
  [ -s "$OUT/stacktrace.txt" ] || rm -f "$OUT/stacktrace.txt"
else
  echo "no pod found with label app=vllm-qwen3-asr" > "$OUT/pod-describe.txt"
fi

# Events
kubectl -n "$NS" get events --sort-by='.lastTimestamp' > "$OUT/events.txt" 2>&1 || true

# Manifests as-applied
kubectl -n "$NS" get deployment vllm-qwen3-asr -o yaml > "$OUT/deployment.yaml" 2>&1 || true
kubectl -n "$NS" get service vllm-qwen3-asr -o yaml > "$OUT/service.yaml" 2>&1 || true

# Secret summary (no values — just shape)
kubectl -n "$NS" get secret hf-token -o json 2>/dev/null \
  | jq '{name: .metadata.name, type: .type, keys: (.data // {} | keys)}' \
  > "$OUT/secret-summary.txt" 2>&1 || true

# Request/response
cp "$DIR/audio-payload.json" "$OUT/request.json" 2>/dev/null || true

# Pull the response body out of cluster-stacktrace.log (between markers)
if [ -f "$DIR/cluster-stacktrace.log" ]; then
  awk '/--- response body ---/{flag=1;next} /---/{flag=0} flag' \
    "$DIR/cluster-stacktrace.log" > "$OUT/response.json" 2>/dev/null || true
  [ -s "$OUT/response.json" ] || rm -f "$OUT/response.json"
  cp "$DIR/cluster-stacktrace.log" "$OUT/cluster-stacktrace.log"
fi

# Summary
echo ""
echo "== Captured =="
ls -la "$OUT" | sed 's/^/  /'
echo ""
echo "Artifact path: $OUT"
[ -s "$OUT/stacktrace.txt" ] && echo "✅ Stacktrace captured — see $OUT/stacktrace.txt"
[ -f "$OUT/stacktrace.txt" ] || echo "ℹ️  No TypeError stacktrace found in pod logs."
