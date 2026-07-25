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

Images, manifests and the filter build. Not yet run end to end: the cluster bring-up hit repeated
TLS failures on a flaky network, so the claim that a request completes through this path is
unverified. Treat the topology as proposed, not demonstrated, until that log evidence exists.
