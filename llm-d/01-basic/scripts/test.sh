#!/bin/bash
# Assumes `podman compose up -d` has been run from this directory.
#
# Note on x-request-id: Envoy uses character 14 of the request_id UUID to
# decide tracing status. Non-UUID request_ids are classified `not_traceable`
# and skip span generation. We use uuidgen (or just let Envoy auto-generate)
# so traces actually flow through Jaeger.
set -euo pipefail

GW="${GW:-localhost:8080}"

echo "== Single request =="
curl -sS "${GW}/v1/completions" \
  -H "Content-Type: application/json" \
  -H "X-Tenant-Id: tenant-a" \
  -H "X-Request-Id: $(uuidgen)" \
  -d '{"model":"qwen-7b-awq","prompt":"hello world","max_tokens":20}' | jq .

echo
echo "== Round-robin spread (10 requests; should hit all 3 vllm-mock pods) =="
for i in $(seq 1 10); do
  curl -sS "${GW}/v1/completions" \
    -H "Content-Type: application/json" \
    -H "X-Tenant-Id: tenant-${i}" \
    -H "X-Request-Id: $(uuidgen)" \
    -d '{"model":"qwen-7b-awq","prompt":"req '$i'","max_tokens":5}' \
  | jq -r '.served_by'
done

echo
echo "== Multi-tenant burst (10 concurrent, mixed tenants) =="
for i in $(seq 1 10); do
  curl -sS "${GW}/v1/completions" \
    -H "Content-Type: application/json" \
    -H "X-Tenant-Id: tenant-$((i % 3))" \
    -H "X-Request-Id: $(uuidgen)" \
    -d '{"model":"qwen-7b-awq","prompt":"burst '$i'","max_tokens":5}' \
    -o /dev/null -w 'served-by=%{header_x-pod-id}|t=%{time_total}s\n' &
done
wait

echo
echo "== Done. =="
echo "  Logs:    podman compose logs -f"
echo "  Traces:  http://localhost:16686 (filter Service: envoy)"
