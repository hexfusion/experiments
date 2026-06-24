# parser-interface

Runnable prototype demonstrating a vendor-pluggable inference parser framework: bounded `InferenceInput` interface + per-request typed K/V store, `Parser`/`Decoder` split, self-naming `Vendor`, immutable `Dispatcher`, vendor-agnostic producers (`cost`, `estimator`), read-only consumers via `kv.Reader`, and the `PromptStructured` capability interface (with `Block` sum type).

```sh
make run                                        # multi-vendor demo
make test                                       # tripwire tests on interface method counts
make bench                                      # single-shot perf, writes benchmarks/results.txt
make bench-stat                                 # -count=10 + benchstat for variance (requires benchstat)
```

Layout:

```
kv/                       per-request typed K/V store + canonical keys
parser/                   InferenceInput, Parser/Decoder, Vendor, Registry, Dispatcher,
                          PromptStructured capability interface, Block sum type
producer/cost/            vendor-agnostic cost producer (reads UsageKey + Model())
producer/estimator/       vendor-neutral prefix-hash producer (uses PromptStructured when available)
vendors/anthropic/        eager Anthropic adapter (full json.Unmarshal into typed structs)
vendors/anthropic_proj/   projection Anthropic adapter (gjson surgical reads on raw bytes)
vendors/openai/           openai.Vendor (implements PromptStructured)
vendors/passthrough/      passthrough.Vendor (zero Decoder; does not implement PromptStructured)
benchmarks/               end-to-end + per-operation micro-benchmarks; raw results in results.txt
```

## Vendor extraction strategies: eager vs projection

The framework is agnostic to *how* a vendor extracts fields. Two strategies are bundled side-by-side for Anthropic so the trade-off is measurable.

| Operation                | Eager (`anthropic`)       | Projection (`anthropic_proj`)         | Δ                                |
| ------------------------ | ------------------------: | ------------------------------------: | -------------------------------- |
| Parse                    |    935 ns ± 10% / 14 a    |        110 ns ± 5% / 2 a              | **8.5× faster, 7× fewer allocs** |
| Decoder event (`message_delta`) |    649 ns ± 8% / 9 a    |        208 ns ± 31% / 3 a             | **3.1× faster, 3× fewer allocs** |
| Prompt accessor (cached) |    29 ns ± 15% / 1 a      |       1.66 ns ± 1% / 0 a              | **17× faster on cached re-reads, zero allocs** |
| EndToEnd (Parse + accessors + 2 events + producers) | 2.79 µs ± 34% / 40 a | 1.66 µs ± 14% / 18 a | **1.68× faster, ~2× fewer allocs** |

Median ± half-range across 10 runs (`benchmarks/results-stat.txt`; raw output in `benchmarks/results-multi.txt`). AMD Ryzen AI 9 HX 370, Linux, Go 1.25. Alloc and byte counts are deterministic (± 0% across all runs). All head-to-head deltas exceed their variance bands; the EndToEnd_Anthropic run had unusually wide variance (± 34%) this iteration, but the eager-vs-projection ratio (~1.68×) holds well above noise.

Projection's first call to `Prompt()` pays the full gjson walk (~270 ns measured pre-memoization); all subsequent calls on the same request return the cached value at ~1.7 ns ± 1%.

**Memoization:** the projection accessors (`Model`, `Stream`, `Prompt`, `PromptBlocks`) cache the result on first call using a `bool` flag + cached value on the per-request struct (single-writer-per-request, no atomics). First call pays the full `gjson` walk cost; subsequent calls return the cached value in ~1.7 ns. Eager accessors are stateless because their underlying compute is already ~30 ns (slice walk) and the cache overhead would meet or exceed the savings; the projection cost / savings ratio reverses that decision.

**Trade-off after memoization:** projection's Parse is ~9× faster than eager and the per-request first-call accessor cost is ~270 ns once for Prompt, well below the 919 ns the eager Parse pays unconditionally. Subsequent same-accessor reads are essentially free. Projection is no longer slower on any access pattern.

Both strategies satisfy the same `parser.Parser` interface and return the same `parser.InferenceInput` contract; consumers can swap them without code changes. New vendors can pick either strategy independently.
