# praxis-epp-filter

A Praxis HTTP filter that gets routing decisions from an ext_proc processor without Praxis
implementing ext_proc.

    client -> Praxis -> [this filter] --HTTP--> epp-http-gateway --ext_proc--> processor
                             |
                             +-- applies destination, forwards the processor's body

It is an out-of-tree crate. Praxis's own tree is untouched; this depends on `praxis-proxy-filter`
by path and registers through `export_filters!`, which is Praxis's documented mechanism for
external filter crates with build-time discovery.

Nothing here is llm-d-specific beyond a default header name. Any conforming ext_proc processor
works; llm-d's EPP is the case it was written against because its behavior is the most demanding.

## Why this exists

Praxis carried an ext_proc callout filter in core through v0.4.0 and removed it on 2026-07-22,
and its separate ext_proc server repo is deprecated. So a plugin written for ext_proc has no
supported path there. This filter closes that gap from the other side: Praxis makes an ordinary
HTTP call and never learns the protocol.

## Two behaviors that are not incidental

The filter declares `BodyAccess::ReadWrite`, and it uses the write half. llm-d's EPP parses the
request into a map and re-serializes it, so key order changes and integers above 2^53 lose
precision. What comes back is the body to forward. Forwarding the original instead silently drops
model rewrites, so the filter replaces the body whenever the gateway reports it changed.

A processor can decline a request. Load shedding and admission control arrive as an immediate
response, which becomes a `FilterAction::Reject` carrying the processor's own status rather than a
routing decision. Routing a request the processor refused would be worse than failing it.

## Configuration

```yaml
filters:
  - name: epp_router
    config:
      gateway: http://127.0.0.1:9100
      path: /v1/chat/completions
      timeout_ms: 5000
      fail_open: false
```

`fail_open` is off by default. A routing filter that silently degrades to whatever the next filter
picks is harder to diagnose than a visible error.

## What it publishes

Into Praxis's structured metadata under the `llmd.epp` namespace: the chosen destination, the time
the processor took, and whether the body was rewritten. Praxis already has namespaced structured
metadata and typed filter state, so this needs no new primitive.

## Status

Compiles against Praxis v0.4.0. The request phase only: it gets a decision and forwards. It does
not yet drive the gateway's duplex session, so the processor sees no response phase and usage
accounting does not happen. That needs a streaming HTTP client against `/v1/session` rather than
the unary `/v1/route`, and it is the next piece.

Not yet run end to end against a live Praxis binary.
