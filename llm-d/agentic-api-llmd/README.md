# Demo: agentic-api on llm-d

**Thesis:** the vLLM-upstream Rust agentic front (`vllm-project/agentic-api`) runs the stateful
agentic loop; llm-d routes each turn over ext_proc. Multi-turn conversations stay on the KV-warm pod
via llm-d's prefix-cache scorer, with no custom signaling. This is the "nice Rust path forward" that
deflects the Praxis replace-Envoy approach: keep ext_proc, use upstream Rust for state, let llm-d route.

## The seam (verified from source)

`agentic-api` (`crates/agentic-server-core`):
- `executor/upstream.rs` POSTs to `{llm_api_base}/v1/responses` (blocking + streaming SSE), optional Bearer.
- `config.rs`: `llm_api_base`, `openai_api_key`, `db_url` (its own conversation/response store).
- CLI (`agentic-server`): `--llm-api-base`, `--openai-api-key`, `--port 9000`, `--db-url sqlite://...`.

**Integration = one flag:** `--llm-api-base <llm-d gateway URL>`. agentic-api owns state + hydration;
each turn sends a stateless `/v1/responses` carrying the hydrated (growing) conversation to llm-d.

```
client -> agentic-api :9000  (Rust: Responses state, hydration, tool loop, own DB)
            |  POST {llm-d-gateway}/v1/responses   (hydrated, growing prompt)
            v
          llm-d gateway (agentgateway) -> EPP (ext_proc: prefix-cache scorer) -> vLLM pod /v1/responses
```

Why it shows llm-d's value: the hydrated prompt is the growing prefix, so the EPP prefix scorer keeps
turn N+1 on turn N's KV-warm pod. agentic-api hydrates; llm-d routes on the prefix; cache stays warm.

## HA / active-active (first-class)

The demo is active-active, not one-of-each: >=2 of every component.

| Component | State | Active-active | Basis |
|---|---|---|---|
| agentic-api | stateful (response store) | N replicas + **shared PostgreSQL** + LB | `config.rs` db_url supports Postgres; ADR-02 |
| EPP | stateless (snapshot) | N replicas + agentgateway health-check + LB, no leader election | Envoy upstream-cluster HA |
| vLLM | KV cache (pod-local) | N pods in the InferencePool | by construction |

**Load-bearing requirement:** agentic-api is the only stateful component -> active-active requires a
**shared** store (PostgreSQL, config not code), never local SQLite. With a shared store the front is
stateless-given-shared-state: any replica rehydrates any conversation; plain LB; no leader election.

**Composition property (logical):** llm-d routes on prefix *content*, not on front-replica identity.
So if turn N is served by front A and turn N+1 by front B, B rehydrates the same history from the shared
store and sends the same growing prefix -> llm-d routes to the same warm pod. **Active-active agentic-api
does not break cache locality.** (Had affinity been keyed on a replica-assigned session-id, it would.)

**Failure demonstrations:** kill a front replica mid-conversation (client retry -> other replica ->
rehydrate -> continue, still on the warm pod); kill an EPP (health-check ejects, `failure_mode_allow`
fails open); kill a vLLM pod (route elsewhere, cold re-prefill = graceful degrade).

## Two phases

**Phase 1 - connectivity (cheap, proves the seam):** one vLLM (or inference-sim) behind llm-d;
agentic-api pointed at the gateway; drive a `/v1/responses` request + a `previous_response_id`
continuation. Success = the stateful loop routes through llm-d and returns a correct response.

**Phase 2 - value (needs real vLLM + KV cache):** 2+ vLLM pods behind llm-d; multi-turn conversation;
show the EPP prefix scorer pins each turn to the warm pod. Measure prefill cost per turn:
O(turns) (warm) vs O(turns^2) (cold/round-robin). One graph = the argument.

## Prerequisites

- `agentic-api` built from source (Rust): `cargo build -p agentic-server`.
- llm-d serving `/v1/responses` through the gateway (agentgateway + EPP + InferencePool + vLLM).
- Phase 2 needs real vLLM (prefix cache); Phase 1 can use inference-sim for connectivity only.

## Run (once env is chosen)

```bash
# llm-d up, gateway URL known as $LLMD_GW (serves /v1/responses)
cargo run -p agentic-server -- \
  --llm-api-base "$LLMD_GW" \
  --db-url "sqlite://./agentic_api.db" \
  --port 9000
# then drive /v1/responses at localhost:9000, continue with previous_response_id
```

## Grounded facts (code-verified 2026-07-12)

- **A. agentic-api re-sends the full growing history every turn.** Rehydrates from its own SQLite,
  strips `previous_response_id` (`executor/rehydrate.rs:76`), upstream struct has no continuation field
  (`types/request_response.rs:37-64`). Turn N+1 = same prefix + new tail -> a backend prefix cache hits.
- **B. llm-d `/v1/responses` prefix chain:** approximate INTACT (`estimate.go:104-117` has a Responses
  case -> `TokenizedPrompt` -> `prefixhash`), precise BROKEN (`renderBackend.produce` `backend.go:135`
  has no Responses case -> nil tokens -> precise scoring skipped).
- **C. `agentic-praxis` is a placeholder stub** (`crates/agentic-praxis/src/lib.rs`, 6-line comment).
  No Praxis routing exists in-repo; llm-d can be the first real gateway integration.

## Decision (on facts, low-risk / in-language)

- **Compose delivers warm-cache-across-turns by config, zero code** (A + B-approx): same growing prefix
  -> consistent pod -> turn N+1 hits turn N's KV cache. This is the demo.
- **Precise KV-block routing = one Go case** at `backend.go:135` (add a `body.Responses` branch to
  `renderBackend.produce`). Small, in-language, upstream-contributable. Not required for the demo.
- **No FFI/CGO/Rust required** for the integration; the language decision is decoupled (internal-GC bench).

## Open

- **Environment** (dagobah down as of 2026-07-12): revive dagobah, or kind + a small real vLLM on the
  3060 for a scaled warm-cache proof (sim has no KV cache).
- Reconcile: is there a private/fork Praxis+agentic-api effort? Upstream `agentic-praxis` is empty.
- Auth passthrough (agentic-api Bearer -> llm-d gateway) if the gateway enforces auth.
