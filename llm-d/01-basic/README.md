# 01-basic

Simplest llm-d shape, end-to-end. Real Envoy + Jaeger; BBR / EPP / vLLM mocked.

## Components

| Real | Mocked |
|---|---|
| Envoy 1.35, Jaeger all-in-one | `bbr-mock` (model extractor), `epp-mock` (header-based picker), `vllm-mock` (synthetic OpenAI responder) |

## Run

```
make 01-up        # podman compose up --build -d
make 01-test      # OpenAI-style completion request
make 01-down      # podman compose down
```

(All three are thin wrappers over `scripts/up.sh` / `test.sh` / `down.sh`.)

- `localhost:8080` — Envoy ingress (`/v1/completions`)
- `localhost:9901` — Envoy admin
- `localhost:16686` — Jaeger UI

## Trace shape

```
envoy: ingress
  ├─ ext_proc → bbr-mock          (bbr.process_body)
  ├─ ext_proc → epp-mock          (epp.pick)
  └─ upstream → vllm-mock-N       (POST /v1/completions)
```

W3C `traceparent` carried in gRPC metadata on the ext_proc bidi stream and in HTTP headers on the upstream hop.

> Envoy uses character 14 of `x-request-id` to encode trace status. Use `$(uuidgen)` or omit the header — non-UUID values get classified `not_traceable` and produce no trace.
