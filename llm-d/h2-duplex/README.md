# h2-duplex

Spike for `design/work/llm-d/gateway-data-contract/RESEARCH.md`.

Question: can a next-hop policy service carry a response that begins before the request body
completes, and can it do that without ever materializing the body. If yes, the ext_proc filter
callout is not required for bidirectional cases, and the objection that plain HTTP forces
WebSockets does not hold.

## Shape

Three roles wired in one process, all speaking plaintext HTTP/2 (h2c), which is the in-cluster
gateway-to-service hop.

    client  ->  policy service  ->  upstream
    streams     parses head,        starts emitting tokens after
    body in     forwards,           the first body chunk, while the
    chunks      relays response     rest of the body is still arriving

The policy service owns the forward, which is the Direction B property. It reads a fixed peek
off the head, extracts routing metadata, forwards the decision as a header, and streams the
body through with a fixed buffer. It never holds the whole body.

## Run

    make test         # the assertions
    make run          # h2c, default 384KB body
    make controls     # h1, non-duplex upstream, delayed first body write
    make scale        # high water vs body size

## Findings

Go 1.25.11, `golang.org/x/net` v0.57.0, loopback, 12 chunks with 20ms spacing.

| Case | First response byte | Last request byte | Duplex |
|---|---|---|---|
| h2c, duplex upstream | 2.6ms | 250.7ms | yes |
| http/1.1, duplex upstream | 2.1ms | 249.7ms | yes |
| h2c, upstream drains first | 252.1ms | 251.1ms | no |

The response starts roughly two orders of magnitude before the request finishes. The
drain-first control reports no duplex, which is what makes the other rows mean something: the
harness discriminates rather than always reporting success.

Body is never materialized. Bytes held at once stay flat as the body grows, because the cost is
peek plus copy buffer and nothing else.

| Request body | Bytes held at once |
|---|---|
| 384 KB | 18 KB |
| 3 MB | 18 KB |
| 12 MB | 18 KB |

For comparison, the ext_proc path holds roughly 3x the body plus the decoded JSON graph across
the scheduling decision, and moves roughly 2x the body across the process boundary.

Two results that corrected assumptions going in:

- HTTP/1.1 also achieves duplex here, given `ResponseController.EnableFullDuplex` on the server
  and an unknown-length request body on the client, meaning the mechanism is not h2-specific.
  h2 is still the right target, because h1 duplex does not survive intermediaries that buffer the
  request body before forwarding (golang/go#22209 is that failure against nginx) and h1 has no
  multiplexing. That distinction is inferred from known proxy behavior, not measured here.
- golang/go#17480 does not reproduce, and headers are not gated on the body at all. With the
  first body write deferred 300ms, the upstream still saw request headers at about 1.3ms. A
  policy service can therefore forward headers and let the upstream begin before any body byte
  exists.

## What this does not prove

Loopback in one process, no real gateway, no real model server, no concurrency, no TLS. It
establishes that the protocol and the Go runtime permit the shape. It says nothing about
behavior under load, through agentgateway or Envoy, or about what retry and outlier detection
cost once the policy service owns the forward.

## Next

Replace the stand-in policy service with a shim in Praxis that calls EPP for the decision, so
the body-owning stage is the Rust data plane and the scoring stage stays where it is. That is
the version worth putting real traffic through. Building it here first keeps the protocol
question separate from the integration question.
