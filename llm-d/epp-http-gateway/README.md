# epp-http-gateway

The EPP-facing edge of the ext_proc emulation layer. Design:
`design/work/llm-d/gateway-data-contract/PLUGIN-BINDING.md`.

A data plane that does not want ext_proc calls this over plain HTTP and gets a routing decision
from an unmodified EPP.

    Praxis (or anything)  --HTTP/2 cleartext-->  epp-http-gateway  --ext_proc gRPC-->  EPP

Neither side changes. EPP keeps its scheduler, its plugins and its datastore, and never learns it
was not called by Envoy. The data plane never implements a bidirectional stream, a per-phase state
machine, or the processing-mode rules about which body-mutation shape is legal where.

## API

One endpoint.

    POST /v1/route
      body:    the request that needs a destination
      headers: x-original-path, x-original-method (optional), plus the client's own headers

    200 OK
      X-Routing-Decision: {"destination":"10.0.0.7:8000","set_headers":{...},"body_modified":true,...}
      body: the request body to forward

The response body is the body to forward rather than an echo. That matters: llm-d re-marshals
every OpenAI-parsed request through a map, so keys come back sorted and the bytes differ from what
was sent. A caller that forwards its own copy silently drops model rewrites. `body_modified` says
whether that happened.

When EPP declines the request, the gateway returns EPP's status and body directly rather than a
decision. Admission control and load shedding arrive this way, so a 429 here means do not forward.

## Why an HTTP hop instead of speaking ext_proc

Because the alternative is asking a data plane to implement a protocol it has declined. Praxis
carried an ext_proc callout filter in core through v0.4.0 and removed it on 2026-07-22, and its
separate ext_proc server repo is marked deprecated as a POC they decided not to pursue.

The cost is honest and worth stating: this adds a hop. An out-of-process binding measures about
1.2x a compiled-in call in `plugin-binding-bench`, reproduced under adversarial audit. Calling EPP
through this gateway is slower than calling EPP over ext_proc directly. The trade is that nobody
has to implement ext_proc.

## Transport

Cleartext HTTP/2 with prior knowledge, so concurrent routing calls multiplex over one connection
instead of one per request. The stream flow-control window is raised from the 64KB default to 1MB,
with 4MB per connection, because multi-turn bodies run past 64KB routinely and the default costs
round trips on flow control alone.

## Tests

    go test ./epp-http-gateway/

Four cases against a fake EPP that behaves the way llm-d's does on the wire, answering headers,
buffering the body, and returning the body chunked as a `StreamedResponse` because
`FULL_DUPLEX_STREAMED` requires it:

- a caller speaking only HTTP gets a destination from EPP, and EPP sees the path and body it sent
- a rewritten body comes back to the caller, with `body_modified` set
- an immediate response propagates as a refusal with EPP's status, carrying no destination
- a 200KB body chunks correctly in both directions

## Not built

The response phase. EPP wants response headers and body for usage accounting, and this gateway
only covers the request phase. A `POST /v1/response` reporting status, headers and usage back to
EPP is the obvious next piece, and it inherits a known constraint: agentgateway discards ext_proc
dynamic metadata attached after response headers, which is where EPP attaches it.

Also not built: any measurement of this edge under load. The 1.2x above is from a different
harness and is indicative, not a measurement of this code.
