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

| Operation                | Eager (`anthropic`)      | Projection (`anthropic_proj`) | Δ                              |
| ------------------------ | -----------------------: | ----------------------------: | ------------------------------ |
| Parse                    |    919 ns ± 5% / 14 a    |        103 ns ± 14% / 2 a     | **8.9× faster, 7× fewer allocs** |
| Decoder event (`message_delta`) |    718 ns ± 4% / 9 a   |        223 ns ± 2% / 3 a      | **3.2× faster, 3× fewer allocs** |
| Prompt accessor          |    29 ns ± 12% / 1 a     |        270 ns ± 3% / 2 a      | **9.3× slower, 2× more allocs**  |
| EndToEnd (Parse + accessors + 2 events + producers) | 2.65 µs ± 6% / 40 a | 1.54 µs ± 14% / 19 a | **1.72× faster, ~2× fewer allocs** |

Median ± half-range across 10 runs (`benchmarks/results-stat.txt`; raw output in `benchmarks/results-multi.txt`). AMD Ryzen AI 9 HX 370, Linux, Go 1.25. Alloc and byte counts are deterministic (± 0% across all runs). All four head-to-head deltas exceed their variance bands by an order of magnitude — real signal, not noise.

**Trade-off:** projection shifts cost out of Parse and into accessors. Parse only retains the raw bytes; field reads happen on demand via `gjson`. For workloads that call each accessor ~once per request (the verified EPP pattern), projection is a clear win on every measured workload. For workloads that call the same accessor many times per request, eager wins because Parse pays the unmarshal once and accessors are pure field reads.

Both strategies satisfy the same `parser.Parser` interface and return the same `parser.InferenceInput` contract; consumers can swap them without code changes. New vendors can pick either strategy independently.
