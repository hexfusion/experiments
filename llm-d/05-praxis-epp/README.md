# 05-praxis-epp

A Praxis data plane getting routing decisions from an unmodified llm-d EPP, without Praxis
implementing ext_proc.

    client -> Praxis (epp_router filter) --HTTP--> epp-http-gateway --ext_proc--> EPP -> vllm-sim

Praxis retired ext_proc in both directions: the core callout filter was removed on 2026-07-22 in
v0.4.1, and the separate `praxis-proxy/extproc` server is marked deprecated. So a plugin written
for ext_proc has no supported path there. This closes the gap from the other side, by putting the
protocol in a layer neither party has to own.

Nothing here is EPP-specific. Any conforming ext_proc processor works; EPP is the case it was
built against because its behavior is the most demanding.

## Prerequisites

`make 03-up` first. This reuses that cluster's EPP, sims, InferencePool, CRDs and agentgateway
rather than standing up a second stack.

## Run

    ./scripts/up.sh

Builds both images, side-loads them into kind (rootless podman cannot pull `localhost/`),
applies the manifests, and port-forwards Praxis to `localhost:18081`.

    curl -s localhost:18081/v1/chat/completions \
      -H 'content-type: application/json' \
      -d '{"model":"food-review","messages":[{"role":"user","content":"hi"}]}'

## What to look for

The point is not that the request succeeds. It is where the decision came from:

- `kubectl -n llm-d logs deploy/praxis-epp` shows the filter calling the gateway and applying a
  destination it did not choose.
- `kubectl -n llm-d logs deploy/epp` shows a normal ext_proc exchange. EPP cannot tell it was not
  called by Envoy.
- `kubectl -n llm-d logs deploy/epp-http-gateway` shows the translation.

## Two details that are not incidental

The gateway targets `epp-headless`, a headless Service added by these manifests, rather than the
existing `epp` ClusterIP. A single gRPC connection multiplexes every stream over one TCP
connection, so a ClusterIP would pin all traffic to one EPP pod however many replicas exist, and
an L4 balancer cannot spread what is only one connection.

The filter declares `BodyAccess::ReadWrite` and forwards the body EPP returned rather than the
one it received. EPP re-serializes every OpenAI-parsed request through a map, so key order
changes; forwarding the original silently drops model rewrites.

## Status

Split, because half of this is demonstrated and half is not.

### Verified: HTTP to a real EPP

A plain HTTP caller reaches an unmodified EPP through the gateway and gets a genuine scheduling
decision. Run against EPP built from `llm-d-router` at `f56f3bd9`, in Kubernetes, with a real
InferencePool selecting three sim pods.

    curl -H 'content-type: application/json' -H 'x-original-path: /v1/chat/completions' \
      --data '{"model":"TinyLlama/TinyLlama-1.1B-Chat-v1.0","messages":[...]}' \
      http://127.0.0.1:19100/v1/route

    HTTP 200
    X-Routing-Decision: {"destination":"10.244.0.11:8000", ...}

`10.244.0.11` was a real sim pod. EPP emitted its ordinary span tree for the request:
`gateway.request`, `gateway.request_orchestration`, `run_scheduler_profile`, `filter_endpoints`,
`pick_endpoints`. That is the same shape it emits behind a real gateway, which is the strongest
available evidence that EPP cannot tell it was not called by Envoy.

The re-marshal defect showed up unprompted. The body sent was
`{"model":...,"stream":false,"messages":[...]}` and the body returned was
`{"messages":[...],"model":...,"stream":false}`: keys reordered by `Director.repackage` going
through a `map[string]any`. Observed against a running binary rather than inferred from source,
and the reason this gateway returns the body to forward instead of an echo.

### Not working: Praxis to the gateway

Praxis serves requests and returns real completions, but `epp_router` does not fire. A request
through Praxis returned HTTP 200 with a valid completion while EPP's `pick_endpoints` span count
did not move, so Praxis proxied straight to the sims and never consulted EPP.

The 200 is a false positive and worth recording as one. It is also an argument for the filter's
`fail_open: false` default: under fail-open, a completely inert filter still returns 200s and
nothing looks wrong.

Known cause so far: the custom server binary never initialises tracing, which Praxis's own
`main.rs` does before calling `run_server`. So Praxis logs nothing, in-cluster or locally, and any
warning about the filter is swallowed. Fix that first, then re-check whether the filter is
registered, whether the chain accepts it, and whether the config keys match what the filter
expects.

### Open design questions this surfaced

Graceful termination: the duplex session holds an ext_proc stream for a whole generation, so a
gateway rolling restart drops in-flight requests unless it drains first.

Retry: whether a retry re-consults EPP or reuses the prior decision is a real semantic choice.
Re-consulting is right for load shedding; reusing is right for idempotency. Neither is
implemented.
