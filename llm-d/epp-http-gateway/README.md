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

Eight cases against a fake EPP that behaves the way llm-d's does on the wire, answering headers,
buffering the body, and returning the body chunked as a `StreamedResponse` because
`FULL_DUPLEX_STREAMED` requires it.

Request phase:

- a caller speaking only HTTP gets a destination from EPP, and EPP sees the path and body it sent
- a rewritten body comes back to the caller, with `body_modified` set
- an immediate response propagates as a refusal with EPP's status, carrying no destination
- a 200KB body chunks correctly in both directions

Session, both phases:

- both phases reach EPP on one stream, asserted by counting streams and checking the response
  phase saw a stream that had already carried a request
- usage survives an event split across two transport chunks
- an aborted stream reports its partial count marked incomplete, and is counted separately
- the decision arrives while the uplink is still open, so the caller can forward without waiting

## The response phase, and why it is one stream

    POST /v1/session          HTTP/2, full duplex

EPP keeps its per-request context on the ext_proc stream. Usage reported on a second stream has
nothing to attach to, so a separate `POST /v1/response` cannot work. The request and response
phases have to share one connection end to end, which is why this endpoint is full duplex rather
than two round trips.

Two logical responses travel down the stream. The first is the routing decision, sent as soon as
EPP produces it so the caller can forward upstream while the stream stays open. The second is the
usage report, sent once the caller has relayed the upstream response back up.

Each direction is framed as `[type:1][length:4][payload]`, which is enough to interleave metadata
and raw body bytes without escaping either. Uplink carries request headers, request body, request
end, then response headers, response body chunks, response end. Downlink carries the decision, the
body to forward, and finally usage.

That framing is deliberately the same shape as gRPC's own wire format, which is a one-byte flag
and a four-byte length over HTTP/2 DATA frames. Bidirectional streaming was never a gRPC feature;
it is an HTTP/2 one that gRPC exposes. Writing the framing directly costs about forty lines and
avoids taking on the ext_proc protobuf contract and its processing-mode rules, which is the part
worth avoiding.

Response chunks are relayed as they arrive rather than reassembled, so EPP sees the event
boundaries the client sees. The SSE decoder is stateful across chunks because splitting per
transport chunk drops any event straddling a boundary, which is a live defect in the current
implementation and the reason streamed usage goes missing there.

Usage carries a `complete` flag. An aborted stream reports what was seen and marks it incomplete,
so a client disconnect does not silently become a token undercount.

## Horizontal scaling

**The gateway holds no state across requests.** Per request it holds one ext_proc stream to EPP,
one SSE decoder, and the buffered request body. None of it survives the request. Replicas need no
coordination, no leader and no shared store, and any replica can serve any request.

**One request pins to one replica for its duration**, because the session holds the EPP stream.
HTTP/2 gives that for free: the session is one stream on one connection, so it is connection
affinity rather than session affinity and needs no sticky routing. If a replica dies mid-request
that request fails, and nothing is left inconsistent because there was nothing to leave behind.

**Reaching EPP needs client-side load balancing, and the obvious dial is wrong.** A single gRPC
connection multiplexes every stream over one TCP connection, so dialling a Service VIP sends all
traffic to one EPP pod however many replicas exist, and an L4 balancer cannot spread what is only
one connection. The fix is a `dns:///` target at a headless service with round-robin, which
resolves every replica and spreads subconnections across them. `praxis/INTEGRATION-POINTS.md`
flags the same hazard from the other direction.

**Both phases necessarily reach the same EPP replica**, since they share one ext_proc stream.
That is required, because EPP's per-request context lives on the stream, and it matches what the
Envoy path already does. No regression.

**EPP's own active-active behavior is unchanged.** Its prefix indexer is per pod, so which replica
a request lands on still changes cache locality, and the hit rate still degrades with replica
count in the way the delta measurements in `plugin-binding-bench` show. This gateway neither
improves nor worsens that.

### The cost of the duplex design

Capacity here is concurrent in-flight generations, not requests per second, and the two differ by
orders of magnitude.

Because the session spans both phases, the gateway holds a downstream HTTP/2 stream and an
upstream gRPC stream for the entire response, not just for the routing decision. A 4000-token
generation at 20ms per token holds both for around 80 seconds. Sizing against a request rate will
be wrong; size against the number of generations expected in flight at once.

Two knobs follow from that. `MaxConcurrentStreams` is set to 1000 here rather than the default
250, and connection count across a fleet is the product of gateway replicas and EPP replicas
under round-robin, so ten gateways against twenty EPPs is two hundred connections. Both are
manageable and neither is free.

A caller that only wants a routing decision and does not need usage reported should use
`/v1/route` instead, which completes immediately and holds nothing.

## Not built

No measurement of this edge under load, and none of the scaling claims above are measured. The
1.2x is from a different harness and is indicative, not a measurement of this code. In particular
the concurrent-generation capacity argument is reasoning from the design, not an observation.

The response-phase path here reports usage to EPP over its own stream, which is correct for this
gateway. It does not address the separate constraint that agentgateway discards ext_proc dynamic
metadata attached after response headers, which is where EPP attaches it. That matters for the
deployed Envoy and agentgateway path, not for this one.
