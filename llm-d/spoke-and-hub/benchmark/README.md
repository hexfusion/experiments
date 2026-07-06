# k6 benchmark: spoke-and-hub router

Attributes each request by decoding the hub's `x-session-token` (`base64("<ns>/<cluster>")`), so no extra
header is needed.

- **affinity** (1 VU = 1 session): replays the token, counts how often a session stays on its first cluster.
- **load** (open-loop): per-cluster latency and shed.

## Run

```bash
HUB=$(kubectl -n aig-routing get gateway hub -o jsonpath='{.status.addresses[0].value}')
podman run --rm -i \
  -e TARGET="https://$HUB" -e MODE=affinity -e CLUSTERS=ifc1,ifc2 -e MODEL=Qwen/Qwen3-1.7B \
  -e VUS=6 -e DURATION=30s -v "$PWD/test.js:/test.js:ro,Z" \
  docker.io/grafana/k6:0.49.0 run --insecure-skip-tls-verify /test.js
```

kind: `MODE=affinity ./run.sh`

Env: `TARGET`, `MODE` (load|affinity), `CLUSTERS` (names to attribute), `MODEL`, `VUS` (sessions),
`RATE` (load req/s), `DURATION`.

## Proven: 100% affinity on AWS (2026-07-06)

Two clusters (`ifc1`, `ifc2`), `qwen3-1-7b`, 6 sessions x 30s:

```json
{ "stickiness_pct": 100, "clusters": { "ifc1": { "n": 190 }, "ifc2": { "n": 165 } }, "unknown": 0, "errors": 38 }
```

Every session stayed on its first cluster; `unknown: 0` (attribution clean); `errors` are one spoke's
degraded GPUs.
