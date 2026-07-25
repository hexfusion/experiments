# plugin-binding-bench

Spike for `design/work/llm-d/gateway-data-contract/RESEARCH.md`.

Question: what does it cost to attach a plugin through a binding instead of compiling it into
the proxy, and does hosting existing ext_proc consumers through an adapter beat running them
natively against Envoy. That is the acceptance criterion for the whole primitive: compiled-in
always wins on raw cost, so the argument only survives if the delta is small and the adapter
still beats the status quo.

## Shape

One plugin, written once, run identically in every arm. Only how it receives its inputs
differs. The plugin is an EPP-shaped scorer that ranks candidate endpoints by load and by
prefix-block overlap, so its cost scales with endpoints times blocks like a real scorer.

The ext_proc arms use the real `go-control-plane` ext_proc v3 protobufs over gRPC, with the
same 62000-byte chunking llm-d-router uses, so the transport is identical across arms and only
the payload differs.

| Arm | Shim parses | Payload to plugin | Body returned |
|---|---|---|---|
| native | once | direct call, no serialization | n/a |
| adapter (metadata + tokens) | once | metadata as JSON, including the token array | no |
| adapter (binary, with tokens) | once | metadata fixed-width binary, including tokens | no |
| adapter (metadata, no tokens) | once | metadata as JSON, token array withheld | no |
| body, no echo | no | full body | no |
| status quo | no | full body, plugin parses it itself | yes, full mutation |

## Workload

Multi-turn, because that is the regime that matters. Each turn resends the whole history plus a
new exchange, so request size grows monotonically and total session bytes are quadratic in turn
count. Default is 40 turns ending around 323KB, mean 162KB, 6.5MB pushed per session. Three
body consumers in the chain, matching the ODH and RHOAI path where `ipp-pre`, `ipp`, and EPP
each open the body.

## Run

    make bench        # default: 40 turns, 3 consumers, 100 endpoints
    make sweep        # plugin cost sensitivity: 5 / 100 / 500 endpoints

## Findings

Go 1.25.11, loopback, 40 turns, 3 consumers, 100 endpoints. Absolute times drift several
milliseconds between runs on a laptop; ratios were stable across three runs. Wire and
allocation figures are deterministic.

| Arm | Per turn | vs native | vs status quo | Wire/session | Alloc/session |
|---|---|---|---|---|---|
| native (compiled in) | 15.94ms | 1.0x | 0.69x | 0 | 44MB |
| adapter (binary, with tokens) | 21.27ms | 1.3x | 0.92x | 13.0MB | 138MB |
| adapter (metadata, no tokens) | 19.63ms | 1.2x | 0.85x | 3.6MB | 75MB |
| body, no echo | 22.36ms | 1.4x | 0.97x | 19.5MB | 256MB |
| status quo (ext_proc full body) | 23.07ms | 1.4x | 1.0x | 38.9MB | 286MB |
| adapter (metadata + tokens, JSON) | 37.12ms | 2.3x | 1.6x | 34.3MB | 448MB |

**The adapter beats the status quo, and the margin is modest.** Binary metadata carrying the
full token sequence runs at 0.92x the status quo on latency, 3x better on wire, and 2.1x better
on allocation. The premise holds, but latency is not where the win is. Bandwidth and allocation
are.

**JSON metadata destroys the premise.** Same information, same transport, JSON instead of
fixed-width: 1.75x to 1.87x slower across three runs, 2.6x the wire, 3.2x the allocation. At
37.12ms it is 1.6x slower than the status quo it is supposed to replace, so an emulation layer
that serializes routing metadata as JSON is worse than doing nothing.

The reason is specific and worth stating precisely, because it is not "JSON is slow." The cost
is concentrated in the token array. Encoding tens of thousands of `uint32` as decimal text with
separators costs more bytes than fixed-width and adds a formatting pass on write and a parse
pass on read. JSON without the token array runs at 19.63ms, within noise of the binary encoding
at 21.27ms. JSON is therefore fine for small scalar metadata and disqualifying for the large numeric
arrays that prefix-cache routing needs.

For raw bytes the question does not arise: ext_proc already carries the body in a protobuf
`bytes` field, copied verbatim with no encoding of the content. JSON would base64 it and add a
third. Proto is strictly correct there and already in use.

**Removing the echo is a bandwidth fix, not a latency fix.** The no-echo arm halves wire
traffic, 38.9MB to 19.5MB, but moves latency only 3 percent and allocation only 10 percent,
because per-consumer parsing dominates both. On loopback the echo is nearly free in time. Over
a real network hop it would matter more, which this harness cannot show.

**Plugin cost decides how the delta reads.** The absolute boundary cost is roughly constant at
2 to 5ms per turn; the ratio to native is what moves.

| Endpoints | native | binary adapter | status quo | adapter vs native | adapter vs status quo |
|---|---|---|---|---|---|
| 5 | 2.56ms | 4.75ms | 7.56ms | 1.9x | 0.63x |
| 100 | 11.67ms | 15.08ms | 20.87ms | 1.3x | 0.72x |
| 500 | 52.88ms | 55.86ms | 62.50ms | 1.1x | 0.89x |

The adapter beats the status quo at every plugin cost. The gap to native is worst for cheap
plugins, where a fixed boundary cost has nothing to hide behind. The binding decision is
therefore per-plugin: a cheap, hot plugin is a poor candidate for an out-of-process binding regardless of
encoding, and an expensive one barely notices.

## Under real Envoy, with concurrency

`make envoy` and `make envoy-load` run the same workload through a real Envoy 1.36.9 on the host
network, so the status-quo arm is the actual thing rather than a model of it. Five paths, same
client, same conversation:

| Listener | What it is |
|---|---|
| bare | Envoy, no ext_proc. Isolates the proxy hop itself |
| status quo | Envoy plus three ext_proc consumers, FULL_DUPLEX_STREAMED, each parses and each returns the body |
| buffered | the same three consumers in BUFFERED, so no body is returned. The mode fix alone |
| shim | Envoy plus one ext_proc consumer that parses once and hosts the three plugins |
| h2c service | no Envoy at all. The shim is a cleartext HTTP/2 service that owns the forward |

20 turns, mean 79KB per request, 50 endpoints.

Eight concurrent sessions:

| Arm | p50 | p90 | p99 | RPS | Errors | p99 vs status quo |
|---|---|---|---|---|---|---|
| envoy bare | 0.4ms | 0.8ms | 1.2ms | 17226 | 0 | 0.04x |
| status quo | 14.5ms | 23.9ms | 28.6ms | 544 | 2 | 1.0x |
| buffered, no echo | 10.9ms | 18.4ms | 22.4ms | 693 | 0 | 0.78x |
| shim, parse once | 7.6ms | 14.0ms | 16.9ms | 982 | 0 | 0.59x |
| h2c service, no envoy | 7.7ms | 13.9ms | 17.1ms | 981 | 0 | 0.60x |

Thirty-two concurrent sessions:

| Arm | p50 | p90 | p99 | max | RPS | Errors | p99 vs status quo |
|---|---|---|---|---|---|---|---|
| envoy bare | 1.4ms | 2.3ms | 3.7ms | 12.2ms | 20122 | 0 | 0.06x |
| status quo | 27.8ms | 47.0ms | 64.4ms | 82.8ms | 1070 | 57 | 1.0x |
| buffered, no echo | 23.9ms | 41.4ms | 53.4ms | 61.8ms | 1262 | 0 | 0.83x |
| shim, parse once | 16.1ms | 28.8ms | 44.7ms | 60.0ms | 1775 | 0 | 0.69x |
| h2c service, no envoy | 16.7ms | 29.5ms | 39.4ms | 43.7ms | 1835 | 0 | 0.61x |

**Envoy is not the cost.** With no ext_proc the proxy hop is 1.2ms to 3.7ms at p99 and serves
17000 to 20000 rps. Everything expensive in the status quo is the ext_proc chain, not the proxy.

**Removing Envoy buys almost nothing.** The h2c service with no proxy in the path and the shim
behind Envoy land within noise of each other at eight concurrent, 17.1ms against 16.9ms p99.
At thirty-two the no-proxy path is better on the tail, 39.4ms against 44.7ms, with a visibly
tighter max. So circumventing the proxy is a tail-latency refinement, not the win. The win is
parse-once and one consumer instead of three, and it is available with Envoy still in place.

**Parse-once beats the mode fix, and both help.** Removing the echo alone gets p99 to 0.78x and
0.83x. Parse-once with a single consumer gets to 0.59x and 0.69x, with 1.7x to 1.8x the
throughput.

**Only the status quo failed under load.** 57 errors at thirty-two concurrent sessions while
every other arm returned zero. The three-consumer echo chain is the arm that breaks first.

The earlier loopback-only numbers understated all of this, exactly as expected: the adapter
measured 0.92x against a modelled status quo and measures 0.59x to 0.69x against the real one.

### At 500 concurrent

Two runs, stable to within a few percent. 24 cores, everything co-resident.

| Arm | p50 | p90 | p99 | RPS | Errors |
|---|---|---|---|---|---|
| envoy bare | 8.9ms | 29.5ms | 53.2ms | 35195 | 0 |
| status quo | 100.4ms | 902.5ms | 1817.8ms | 1353 | 255 |
| buffered, no echo | 118.6ms | 843.6ms | 1586.7ms | 1388 | 0 |
| shim, parse once | 71.8ms | 831.3ms | 1977.6ms | 1852 | 0 |
| h2c service, no envoy | 132.1ms | 587.6ms | 1319.8ms | 1873 | 0 |

Everything except bare Envoy is saturated here, so read throughput and ignore the tail. RPS
plateaus between 1350 and 1875 while bare Envoy serves 35000, which puts the bottleneck
squarely in the consumers rather than the proxy. Envoy scales; the ext_proc chain does not.

Throughput ordering is stable and matches the lower-concurrency runs: parse-once carries about
1.37x the status quo. The shim's p99 goes slightly worse than the status quo at this point,
which is a saturation artifact rather than a regression, since it is accepting 37 percent more
work into the same queues. The no-proxy path holds the best tail at every concurrency tested.

### Would a Rust shim change this

Per request, on an 85KB mid-conversation body:

| Stage | Time | Allocations |
|---|---|---|
| encoding/json unmarshal alone | 309us | 89KB, 57 allocs |
| full parse, unmarshal plus tokenize plus block keys | 614us | 562KB, 2487 allocs |
| one plugin scoring | 1181us | 0 |
| shim path, parse plus three plugins | 4287us | 562KB, 2487 allocs |

The parse is 14 percent of the shim's CPU and JSON unmarshalling specifically is 7 percent. The
other 83 percent is the plugins scoring, which a Rust shim does not change, because the plugin
is the thing being hosted rather than rewritten. Rewriting the shim in Rust therefore cannot
move the saturation point much at this plugin cost.

Where it would help is allocation. The parse produces 562KB and 2487 allocations for an 85KB
body, a 6.6x amplification, while scoring allocates nothing. At 1850 rps that is roughly a
gigabyte per second of garbage attributable entirely to the parse. That is the plausible source
of tail variance and it is the part a non-GC language removes.

The caveat that keeps this honest: the 14 percent figure is relative to a scorer whose cost was
chosen for this harness. A cheaper plugin raises the parse share. The stable conclusion is not a
percentage but an ordering: parsing once instead of three times is worth more than changing the
language the parse is written in.

### Response path

`-resp-chunks` and `-chunk-delay` turn the upstream into a real SSE stream: N content chunks
plus a `stream_options`-style usage chunk plus `[DONE]`. The status-quo consumers each decode
the stream for themselves and mutate every chunk, as EPP does with `rewriteModelName`. The shim
decodes once and publishes response metadata. The decoder is stateful across chunks on purpose,
because a per-chunk split drops any event straddling a transport boundary.

**Measure the crossings, not the events.** With no inter-token delay the upstream's chunks
arrive back to back and Envoy coalesces them: the shim saw 11 ext_proc response messages for 65
SSE events, so a zero-delay run understates per-chunk cost roughly sixfold. With a 2ms
inter-token delay the ratio becomes 34 messages for 33 events, which is what a real token stream
looks like. Every response-path number below uses the delayed form.

32 chunks, 2ms inter-token delay, 10-turn conversations, eight concurrent:

| Arm | p50 | p99 | TTFT p50 | TTFT p99 | RPS |
|---|---|---|---|---|---|
| envoy bare | 75.7ms | 80.3ms | 0.9ms | 1.7ms | 105 |
| status quo | 98.3ms | 112.1ms | 12.0ms | 24.8ms | 82 |
| buffered, no echo | 96.7ms | 106.2ms | 12.1ms | 18.7ms | 82 |
| shim, parse once | 88.8ms | 97.0ms | 7.9ms | 14.6ms | 90 |
| h2c service, no envoy | 80.2ms | 89.5ms | 4.9ms | 11.4ms | 100 |

**End-to-end latency stops discriminating once the response streams.** 32 chunks at 2ms is a
64ms floor every arm pays, so total p99 compresses toward parity and the shim reads 0.87x rather
than the 0.50x it shows on a non-streamed response. That is an artifact of the floor, not a
change in processing cost.

**Time to first byte is the metric that survives.** TTFT p50 goes 12.0ms for the status quo,
7.9ms for the shim (0.66x), and 4.9ms for the no-proxy service (0.41x). TTFT is dominated by the
request-side parse, which is exactly what parse-once removes, so the improvement carries over
from the request-only runs intact.

**Per-chunk response crossings are cheap.** Three consumers at 1:1 crossings pay about 100
boundary crossings per request against the shim's 34, and the whole difference between bare
Envoy and the status quo is 22.6ms spread over 33 chunks, roughly 0.2ms per crossing per
consumer. The response path did not overturn the request-path conclusion; it relocated it from
total latency to TTFT.

## What this does not prove

Single host, loopback networking, a stand-in upstream that discards the body rather than a model
server, Go rather than Rust, and no response-path processing at all. The hosted plugins in the
Envoy arms use the native binding, so these numbers show the ceiling for hosting rather than the
cost of an out-of-process binding underneath the shim; the in-process table above is where that
cost lives.

## ext_proc gotchas this harness encodes

Each of these cost a debugging cycle and each is a mode-versus-mutation mismatch:

- A `StreamedResponse` body mutation is only valid under `FULL_DUPLEX_STREAMED`. Returning one
  in `BUFFERED` corrupts the body for downstream filters, which surfaces as a JSON parse error
  in the second or third consumer rather than anywhere near the cause.
- On the response path in `STREAMED` mode the correct mutation is a plain `Body`, not a
  `StreamedResponse`. Getting that wrong stalls every request at almost exactly one second.
- The server must answer `RequestHeaders` or Envoy waits for a response that never comes.

## Next

A hosted wasm consumer, to test whether the Kuadrant-shaped case is cheaper hosted or left on
the chain. A driver on a separate host, since at 500 concurrent the client competes with
everything it measures. And the two-directional `stream_options` case, where usage accounting
requires injecting the option into the request and stripping the extra chunk from the response.
