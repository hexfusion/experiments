# agentic-api on llm-d: tokens all the way down

Demo of the AI-Gateway design on **real llm-d components**: parse and tokenize
the request body once, then tokens flow to routing and inference with no
re-render, with MaaS governance wrapping the flow as an Envoy ext_proc filter.

```
client
  -> Envoy [ext_proc: IPP]          governance: payload guardrails (real payload-processor)
  -> agentic-api :9000              Rust, upstream: conversation + tool loop, own store
  -> Coordinator :8080              Go, llm-d-router: tokenize the delta once, session cache
  -> EPP                            route on token_ids (short-circuits re-tokenize)
  -> vLLM                           token-in inference
```

Every box is a real, shipping component. The Coordinator and EPP live in
`llm-d/llm-d-router`; agentic-api is `vllm-project/agentic-api`; the IPP is
`llm-d/llm-d-inference-payload-processor`.

## What it demonstrates

- **Tokenize once.** The Coordinator tokenizes the body and ships
  `/v1/completions` with `token_ids`; the EPP reuses them (no re-render); vLLM
  runs token-in. (vLLM `/v1/responses` ignores injected tokens, so Responses
  inference is routed through `/v1/completions` and adapted back.)
- **Delta multi-turn (O(turns), not O(turns^2)).** agentic-api sends only each
  new turn plus `x-session-id`; the Coordinator keeps a session token cache and
  renders only the delta. Engine input tokens grow (31, 98, 164, ...) while the
  client sends one turn each time.
- **Cache-miss recovery (approach B).** A continuation whose prefix the
  Coordinator lost (eviction, restart) returns `session_miss` (409); agentic-api
  transparently retries with full history and reseeds. Delta is an optimization
  over a correct floor. See the design repo's
  `work/llm-d/ai-gateway/DELTA-RECOVERY.md`.
- **MaaS governance as ext_proc.** The real payload-processor runs as an Envoy
  `ext_proc` filter (`FULL_DUPLEX_STREAMED`, fail-closed) in front of the flow.
  It is a side-filter (Envoy routes; the IPP reads/mutates/blocks), not a
  service-caller. Auth would be the sibling `ext_authz` -> Authorino (Keycloak
  IdP), out of scope here.

## Layout

- `agentic-api.yaml`, `agentic-Dockerfile`, `agentic-dockerignore` - agentic-api
  deploy + image (Level-2 delta mode via `DELTA_UPSTREAM=true`).
- `coordinator/` - the real Go Coordinator deploy, a synthetic Level-2 client
  (`level2-test.py`, delta drift check), and `session-miss-test.sh` (proves B on
  the Coordinator: restart mid-session -> 409 -> reseed).
- `ipp/` - the real ext_proc IPP: `config.yaml` (PayloadProcessorConfig) +
  `deploy.yaml` (IPP + Envoy with the `ext_proc` filter wired in front of
  agentic-api).
- `agentic-b-e2e.sh` - end-to-end proof of B through the real client: multi-turn
  conversation, Coordinator restarted mid-conversation, next turn recovers
  coherently.
- `diagrams/` - `delta-chain` (the request/token flow) and `full-arch` (the
  trusted-Envoy-data-plane + modular AI callouts view).

## Run

Namespace `agentic-demo`. Bring up vLLM, the Coordinator (`coordinator/deploy.yaml`),
the EPP, agentic-api (`agentic-api.yaml`), and the IPP + Envoy (`ipp/deploy.yaml` +
`ipp/config.yaml`), then:

```bash
bash coordinator/session-miss-test.sh   # B at the Coordinator (direct)
bash agentic-b-e2e.sh                    # B end-to-end through agentic-api
```

## Code changes behind this demo (uncommitted forks, not upstream yet)

- `llm-d-router`: Coordinator `/v1/responses` entry + session-cache delta +
  completions token-in adapter; delta-mode `session_miss` recovery; session-id
  underscore validation fix (independent bug).
- `agentic-api`: Level-2 delta upstream (`--delta-upstream`); the client-side
  `session_miss` retry (`x-session-seq` + full-history fallback).

## Upstream contributions identified

- vLLM: token-in on `/v1/responses` (removes the completions adapter and closes
  the assistant re-tokenize drift via `return_token_ids`).
- agentic-api: session id in the response store so `previous_response_id` chains
  carry a stable session key.
