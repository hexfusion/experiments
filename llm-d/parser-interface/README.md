# parser-interface

Runnable prototype demonstrating a vendor-pluggable inference parser framework: bounded `InferenceInput` interface + per-request typed K/V store, `Parser`/`Decoder` split, self-naming `Vendor`, immutable `Dispatcher`, vendor-agnostic producers (`cost`, `estimator`), read-only consumers via `kv.Reader`, and the `PromptStructured` capability interface (with `Block` sum type).

```sh
go run .                                        # multi-vendor demo
go test ./parser/                               # tripwire tests on interface method counts
go test -bench=. -benchmem ./benchmarks/        # perf
```

Layout:

```
kv/                       per-request typed K/V store + canonical keys
parser/                   InferenceInput, Parser/Decoder, Vendor, Registry, Dispatcher,
                          PromptStructured capability interface, Block sum type
producer/cost/            vendor-agnostic cost producer (reads UsageKey + Model())
producer/estimator/       vendor-neutral prefix-hash producer (uses PromptStructured when available)
vendors/anthropic/        anthropic.Vendor (implements PromptStructured)
vendors/openai/           openai.Vendor (implements PromptStructured)
vendors/passthrough/      passthrough.Vendor (zero Decoder; does not implement PromptStructured)
benchmarks/               end-to-end + per-operation micro-benchmarks; raw results in results.txt
```
