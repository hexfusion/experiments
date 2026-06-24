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

| Operation                | Eager (`anthropic`) | Projection (`anthropic_proj`) | Δ                        |
| ------------------------ | ------------------: | ----------------------------: | ------------------------ |
| Parse                    |      1062 ns / 14 a |                   89 ns / 2 a | **12× faster, 7× fewer allocs** |
| Decoder event (`message_delta`) |        741 ns / 9 a |                  208 ns / 3 a | **3.6× faster, 3× fewer allocs** |
| Prompt accessor          |         28 ns / 1 a |                  282 ns / 2 a | **10× slower, 2× more allocs** |
| EndToEnd (Parse + accessors + 2 events + producers) |      2608 ns / 40 a |                1458 ns / 19 a | **1.8× faster, ~2× fewer allocs** |

Numbers from `benchmarks/results.txt` on AMD Ryzen AI 9 HX 370 (Linux, Go 1.25).

**Trade-off:** projection shifts cost out of Parse and into accessors. Parse only retains the raw bytes; field reads happen on demand via `gjson`. For workloads that call each accessor ~once per request (the verified EPP pattern), projection is a clear win on every measured workload. For workloads that call the same accessor many times per request, eager wins because Parse pays the unmarshal once and accessors are pure field reads.

Both strategies satisfy the same `parser.Parser` interface and return the same `parser.InferenceInput` contract; consumers can swap them without code changes. New vendors can pick either strategy independently.
