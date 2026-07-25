# http-epp-sketch

A sketch of the decider as a plain HTTP service instead of an ext_proc filter.
Mock IPP, mock EPP, mock pods, real HTTP between them. Stdlib only.

Run it:

```
go test -v -count=1 ./...
```

## What it pins down

The API in `api.go` is the actual artifact. The test it has to pass is that its
shape reveals nothing about ext_proc: no phases, no processing modes, no stream.
One request, one decision, plus a report when the request finishes.

## Scenarios

`TestActiveActive` confirms plain HTTP round-robin reaches every replica.
Decisions land 27/31/32 across three replicas over real `httptest` servers.

`TestDivergence` is the reason the sketch exists. Six replicas, eight pods,
concurrency 24, and a scrape interval longer than the run, so each replica has
only its own decisions to go on. Without decision broadcast the load lands
`[82 66 65 27 0 0 0 0]` with a peak of 11 against an ideal of 3: half the fleet
never gets a request, because every replica independently believes the same idle
pods are idle. With broadcast it lands `[35 29 32 30 31 27 28 28]`, peak 5.

That is the case for broadcasting decisions rather than fanning out requests.
Fanning out the request would cost N times the scoring work and still leave
replicas believing they routed somewhere they did not.

`TestMetadataPath` protects the property worth keeping from ext_proc: the decider
decides without ever receiving the body. Three carriers, 90 requests each.

```
body    :  339200 bytes to EPP, 331470 bytes of body received, 90 parses
json    :   13276 bytes to EPP,      0 bytes of body received,  0 parses
headers :   10790 bytes to EPP,      0 bytes of body received,  0 parses
```

Headers with no entity is 31.4x cheaper than forwarding the body and 1.23x
cheaper than sending a JSON document, and prefix affinity still works in both.
The 1.23x is conservative: it counts raw header bytes, and HPACK indexes repeated
names on a persistent connection, so steady state favours headers further.

## Carrying metadata over a plain HTTP request

Two different things are called metadata and only one of them matters here. gRPC
ctx metadata is HTTP/2 headers with a Go API on it. The ext_proc channel is not
that: `metadata_context` and `attributes` on ProcessingRequest, and
`dynamic_metadata` on ProcessingResponse, are all protobuf message fields in the
payload.

The question is therefore which carrier replaces a payload field, and the answer
is headers with no body. Everything the routing decision needs is a flat scalar:
model, target model, prompt token count, prefix hash, session, priority.

The reason to prefer headers over a JSON document is not the byte count. It is
that the two-route topology has no choice: when IPP hands the request back to the
gateway, the body is the user's request continuing upstream, so derived data must
ride in headers. Using headers for the direct call too means one encoding for
both topologies instead of two.

It also upgrades the type. `structpb.Struct` is untyped JSON wearing protobuf,
with no schema and no validation.

The limit is the header budget, roughly 8 to 16KB. Scalars go inline; bulk
derived data such as token ID arrays or per-block hashes goes by reference into a
shared store instead.

## Validation, not base64

Model name and session id come out of the user's request body and go into a
header, which makes an unvalidated value a header-injection primitive. Go's
net/http rejects bad field values on write, but the guarantee should not depend
on which implementation in the chain stamps the header.

Encoding does not fix that. Base64 carries hostile input safely rather than
refusing it, costs 33% expansion, and flattens the byte distribution so HPACK's
Huffman coding stops helping, which undercuts the reason headers were chosen.
Validation is cheaper and it rejects rather than transports.

Both ends check. The encoder refuses to stamp a bad value and the decoder refuses
to trust one, so a forged header arriving from elsewhere in the chain fails the
same test. `TestForgedHeadersRejected` covers the case a well-behaved caller
cannot produce.

Identity and hints are treated differently. A malformed prefix hash drops the
attributes and routing degrades to load-only, which is what lets a caller that
publishes no metadata keep working. A malformed request id or model is an error,
because there is nothing to fall back to.

Reserve the `-bin` suffix convention from gRPC if a genuinely binary value ever
needs to go inline. Nothing in the current set does.

## What it does not model

No real EPP, no real scheduling, no kube. Load-aware scoring is reduced to least
believed in-flight, and prefix affinity to a single hash. The 32.4x is bytes on
the wire and is not a latency claim.

Scrape interval is deliberately longer than the run in `TestDivergence`, which
isolates inter-scrape behaviour. Real scrapes would correct the herd
periodically, so treat the peak as the worst case within a scrape window rather
than a steady state.
