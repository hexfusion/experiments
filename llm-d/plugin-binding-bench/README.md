# plugin-binding-bench

Spike for `design/work/llm-d/gateway-data-contract/RESEARCH.md`.

Question: what does it cost to attach a plugin through a binding instead of compiling it into
the proxy, and does hosting existing ext_proc consumers through an adapter beat running them
natively against Envoy. That is the acceptance criterion for the whole primitive: compiled-in
always wins on raw cost, so the argument only survives if the delta is small and the adapter
still beats the status quo.

## Results status, 2026-07-25

An adversarial audit found a modelling error that invalidates the headline. Do not cite the
2.8x, the 8.1x, the 80 percent delta hit rate, or any RPS figure.

**The status-quo chain does not tokenize three times.** `scorer.ParseBodyThenDecide` routes every
status-quo consumer through the same full extraction the shim uses, including the render call. In
the real chain only EPP tokenizes: GIE's BBR proposal describes BBR as extracting the model name
and setting a header, and the `ipp` chain is model, header and key rewriting. Corrected to one
tokenizing consumer and two model-name-only consumers, single ownership measures about 1.12x
rather than 2.8x at one attempt, and reentrance drops from 8.1x to 3.7x at four.

**Also refuted or unbacked.** The render concurrency limit in `render.go` is never reached,
because `main.go` drives the benchmark from a single goroutine, so the reentrance superlinearity
is Go GC pressure rather than queueing. The delta hit rate is the closed form `1 - R/T` at the
chosen replica and turn counts; production-shaped values give nearer 10 percent. `SessionCache`
compares message counts rather than content, so a branched history takes the fast path and
silently yields wrong tokens, and it is unbounded at roughly 217KB per session per replica. RPS
is algebraically concurrency over mean latency. The workload's repeated literal strings yield 9
distinct block keys, so `-cache-depth` is inert and the producer-cost sweep measures empty-map
against full-walk. Token ids are 32-bit hashes averaging 9.8 decimal digits against a real vocab's
6, inflating the JSON penalty by roughly 1.6x. `Metadata.Prefix` is `json:"-"`, so the hosted
plugin never runs the prefix branch and the arms are not running identical plugin work.

**What survived.** The adapter at about 1.2x against compiled-in native, reproduced
independently. The response-path shape, per-chunk for the status quo and flat for single
ownership, with magnitude falling from 535ms to roughly 180ms. And the echo fix not being where
any win is.

**Fix order before re-publishing:** per-consumer capability so only one consumer tokenizes; a
concurrent open-loop driver so the render limit is real and a capacity number exists; workload
entropy plus a bounded vocab, which repairs cache-depth, producer cost and the JSON multiplier at
once. Then add the missing arm: status quo plus a dynamic-metadata publisher, which is the design
doc's own first deliverable and may land within noise of the shim.

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

Go 1.25.11, loopback, 40 turns, 3 consumers, 50 endpoints, 50 percent cache depth. Absolute
times drift between runs; ratios are stable. Wire and allocation are deterministic.

### Where the CPU actually goes

Per request on an 85KB mid-conversation body:

| Stage | Time | Allocations |
|---|---|---|
| encoding/json unmarshal alone | 304us | 89KB, 57 allocs |
| parse: unmarshal plus tokenize plus block keys | 681us | 562KB, 2487 allocs |
| extraction: parse plus prefix producer | 760us | 563KB, 2492 allocs |
| prefix producer alone, cold cache | 6ns | 0 |
| prefix producer alone, warm | 21-22us | 328B, 3 allocs |
| one Scorer over 50 endpoints | 285ns | 0 |

**Extraction is essentially all of it and the plugins are noise.** Three Scorers cost under a
microsecond against a 760us extraction stage. JSON unmarshalling alone is about 45 percent of
extraction. The producer confirms prefix cost varies by three orders of magnitude with cache
state, 6ns cold against 22us warm, because it breaks at the first uncached block; even warm it
is about 3 percent of extraction.

This is the opposite of what an earlier version of this harness reported. The earlier scorer was
roughly 4000x too expensive and did the producer's work inside Score.

### Render as a service, and reentrance

`-render` puts tokenization behind a service; `-attempts` makes one client request fan out to N
upstream attempts, as retry, best-of-N, and inference-time scaling do. Render call counts are
reported directly, because the count is the mechanism.

Under single ownership the shim extracts once and reuses the result across attempts. In the
status quo nobody owns the bytes, so all three consumers re-extract on every attempt.

| Attempts | Render calls, single ownership | Render calls, status quo | Native | Binary adapter | Status quo | Status quo vs adapter |
|---|---|---|---|---|---|---|
| 1 | 20 | 60 | 7.86ms | 8.51ms | 23.70ms | 2.8x |
| 2 | 20 | 120 | 7.37ms | 10.28ms | 40.40ms | 3.9x |
| 4 | 20 | 240 | 7.56ms | 12.55ms | 101.06ms | 8.1x |

Per turn, 20 client requests, render concurrency 8, 2ms fixed per call.

**Render calls stay flat under single ownership and scale as 3 times attempts otherwise.** That
is the whole mechanism, visible directly rather than inferred from a latency number.

**The status quo grows superlinearly.** Four times the attempts costs 4.3x the time, because
concurrent render calls exceed the service's limit and queue. The gap widens from 2.8x to 8.1x
across the range tested and is still widening at the end of it.

**Native stays flat and the adapter does not.** Native holds ~7.5ms whatever the fan-out, since
extraction happens once and the plugins are noise. The adapter climbs from 8.51ms to 12.55ms
because it re-ships metadata to three consumers on every attempt where native passes a pointer.
The native in-process binding is the one that has to be excellent; the adapter is for
compatibility.

**The echo fix is noise once render is in the picture**, at 0.96x the status quo, because render
dominates everything the mode change touches.

### The adversarial case: many small requests

`-workload=agentic` generates a long session of bounded requests rather than one growing body,
which is what an agent loop with context compaction produces. If single ownership only wins on
bytes, it should lose here. 200 requests of 3KB, render enabled:

| Arm | Render calls | Per request | vs native | vs status quo |
|---|---|---|---|---|
| native (compiled in) | 200 | 2.57ms | 1.0x | 0.30x |
| adapter (JSON, no tokens) | 200 | 2.89ms | 1.1x | 0.34x |
| adapter (binary, with tokens) | 200 | 3.03ms | 1.2x | 0.36x |
| adapter (JSON, with tokens) | 200 | 3.54ms | 1.4x | 0.42x |
| body, no echo | 600 | 8.37ms | 3.3x | 0.99x |
| status quo | 600 | 8.46ms | 3.3x | 1.0x |

**It survives, and for a reason that is not per-byte.** The status quo still runs 2.8x the binary
adapter, the same multiple as the growing-chat workload, because render call count is three times
one regardless of how large the bodies are. The amortization is per-call, not per-byte, which is
why shrinking the bodies by two orders of magnitude does not move it.

**The encoding result does not generalize, and that is a correction.** JSON against binary
collapses from 6.2x on growing chat to 1.17x here, because a 3KB body yields few tokens and few
block keys, so there is no large numeric array to encode badly. JSON with tokens withheld is
actually the fastest adapter at this size. The encoding finding is a large-body result and should
be stated as one.

**The adapter closes on native** at 1.2x rather than 1.4x, since serializing small metadata is
cheap. The binding gap is a function of metadata size, so it is widest exactly where delta
extraction is also most valuable.

### Incremental delta extraction, and horizontal scaling

`-delta` measures extracting only what a replica has not already seen. Multi-turn chat resends
the whole history, so turn N+1 is turn N plus a new exchange and everything already extracted
stays valid. `TestDeltaMatchesFull` and `TestDeltaAcrossReplicas` gate this: the delta result is
byte-identical to a full extract, because a session cache that can disagree with a full parse
would be a new instance of the bug the design exists to prevent.

The deployment today is active-active with no session affinity, so the harness models several
replicas each holding its own cache and no shared state.

40 turns, 4 sessions, render concurrency 8, 2ms per call:

| Arm | Total | Render calls | Render MB | Delta hit |
|---|---|---|---|---|
| full extract every turn | 1483ms | 160 | 25.3 | n/a |
| delta, 1 replica | 555ms | 160 | 1.3 | 98% |
| delta, 2 replicas, spread | 624ms | 160 | 2.5 | 95% |
| delta, 4 replicas, spread | 727ms | 160 | 4.6 | 90% |
| delta, 8 replicas, spread | 876ms | 160 | 8.4 | 80% |
| delta, 8 replicas, affinity | 584ms | 160 | 1.3 | 98% |

**Delta shrinks calls rather than eliminating them.** Render call count is 160 in every arm, one
per turn. What changes is call size, 25.3MB of payload down to 1.3MB. This is a different
mechanism from reentrance, where single ownership removes calls outright.

**Active-active degrades gracefully.** A replica is not restricted to continuing from the
immediately preceding turn: since the request carries the whole history, any replica holding
state at message count M can extend to any later turn, rendering the gap rather than one turn. A
replica therefore misses only on its first sighting of a session, which is why 8 replicas over 40
turns still hit 80 percent. Render bytes scale with replica count rather than with conversation
length, so an 8-way spread still saves 3x against the baseline.

**Affinity helps and is not required.** At 8 replicas it is worth 1.5x on wall time and 6x on
render bytes. That is an argument for the session-affinity scorer, not a precondition.

**The bound is the local parse.** Wall time improved 2.7x while render bytes improved 19x,
because extraction still unmarshals the whole body every turn to read the envelope and count
messages. Delta removes the quadratic render growth and leaves the quadratic parse growth.
Removing that as well needs incremental JSON parsing.

### Bindings

| Arm | Per turn | vs native | vs status quo | Wire/session | Alloc/session |
|---|---|---|---|---|---|
| native (compiled in) | 1.81ms | 1.0x | 0.29x | 0 | 44MB |
| adapter (binary, with tokens) | 2.62ms | 1.4x | 0.42x | 13.0MB | 136MB |
| adapter (JSON, no tokens) | 3.31ms | 1.8x | 0.53x | 3.7MB | 76MB |
| body, no echo | 5.93ms | 3.3x | 0.94x | 19.5MB | 267MB |
| status quo (3 consumers, full body) | 6.30ms | 3.5x | 1.0x | 38.9MB | 288MB |
| adapter (JSON, with tokens) | 16.19ms | 8.9x | 2.6x | 35.5MB | 468MB |

**Single ownership is worth 2.4x.** The status quo pays three real extractions where the shim
pays one, so the binary adapter runs at 0.42x. The boundary cost against compiled-in is 1.4x,
which is the number the primitive has to be worth.

**JSON is disqualifying, by 6.2x.** Same metadata, same transport: 16.19ms against 2.62ms. The
JSON adapter is 2.6x worse than the status quo it would replace, so an emulation layer that
serialises routing metadata as JSON is worse than doing nothing.

The sharpest version: JSON without the token array (3.31ms) is worse than binary with it
(2.62ms). Encoding roughly 800 block keys as decimal text costs more than encoding all the
metadata fixed-width. The problem is numeric-array encoding, not payload size.

For raw bytes the question does not arise: ext_proc already carries the body in a protobuf
`bytes` field, copied verbatim. JSON would base64 it and add a third.

## Under real Envoy, with concurrency

**Stale: these tables predate the scorer fix and have not been re-run.** They were measured with
the cartesian-product scorer, so the plugin work inflates every arm and compresses the ratios
between them. The response-path tables above are post-fix and current. Re-run with `make envoy`
before citing anything here.

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

Re-derived after the scorer fix, and the answer reversed.

Extraction is about 99 percent of modelled CPU and the plugins are under 1 percent. JSON
unmarshalling alone is roughly 45 percent of extraction, and the parse produces 562KB and about
2500 allocations for an 85KB body while scoring allocates nothing. So on the CPU this harness
models, a Rust shim addresses nearly all of it, not the 14 percent an earlier version of this
file claimed.

The caveat that keeps this honest is the missing render stage. Real EPP tokenization is a
synchronous network call, and if end-to-end throughput is bound on that, CPU is not the binding
constraint whatever language it is written in. The defensible statement today: Rust addresses
almost all of the CPU we model, and we have not yet modelled the thing that probably dominates.

### Response path

`-resp-chunks` and `-chunk-delay` turn the upstream into a real SSE stream: N content chunks plus
a `stream_options`-style usage chunk plus `[DONE]`. Status-quo consumers each decode the stream
for themselves and mutate every chunk, as EPP does with `rewriteModelName`. The shim decodes once
and publishes response metadata. The decoder is stateful across chunks on purpose, because a
per-chunk split drops any event straddling a transport boundary.

**Measure the crossings, not the events.** With no inter-token delay Envoy coalesces: the shim
saw 11 ext_proc response messages for 65 SSE events, understating per-chunk cost roughly
sixfold. At 1ms the ratio is 1:1 and stays 1:1 at every length tested, verified up to 1026
messages for 1025 events. Every number below uses the delayed form.

Three turns, eight concurrent, 1ms inter-token delay:

| Chunks | Bare p99 | Status quo p99 | Shim p99 | h2c p99 | Status-quo overhead | Shim overhead |
|---|---|---|---|---|---|---|
| 32 | 43.2ms | 60.8ms | 45.0ms | 42.6ms | 17.6ms | 1.8ms |
| 256 | 306.4ms | 425.3ms | 337.7ms | 319.2ms | 118.9ms | 31.3ms |
| 1024 | 1303.1ms | 1837.9ms | 1334.6ms | 1272.9ms | 534.8ms | 31.5ms |

**The ratio is stable and the ratio is misleading.** p99 against the status quo sits at 0.73x to
0.79x at every length, because the streaming floor grows along with everything else. The
overhead columns are the real result: the status quo's response cost is per-chunk and scales
linearly at roughly 0.5ms per chunk, while the shim's overhead stays flat at about 31ms, which
is just its request-side extraction. Its response path is close to free per chunk.

At 1024 output tokens that is 535ms of overhead against 31ms, a 17x gap, and it keeps widening
with output length. Production output runs 500 to 2000 tokens and 4k to 32k for reasoning models.

**So the response path is a growing total-latency story, not a TTFT story.** An earlier version
of this file concluded the opposite, and that was an artifact of capping output at 32 chunks,
which is too short for the per-chunk term to surface. TTFT does improve, 7.1ms to 2.4ms at 1024
chunks, but it is now the smaller half of the argument.

**The echo fix does nothing here.** Buffered-no-echo sits at 1.00x to 1.01x the status quo at
every chunk count. The response-side cost is three consumers each decoding and mutating every
chunk, which the mode fix does not touch.

At 1024 chunks the no-proxy h2c service came in slightly faster than bare Envoy, 1272.9ms
against 1303.1ms, putting its per-chunk relay cost at or below Envoy's own.

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
