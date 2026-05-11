# 02-vllm-sim

Adds real `llm-d-inference-sim` and in-process `llm-d-kv-cache` indexer to the 01 shape. `epp-mock` now does real prefix-cache-aware routing via ZMQ KVEvents from the sims.

## Components

| Real | Mocked |
|---|---|
| Envoy 1.35, Jaeger | `bbr-mock` |
| `llm-d-inference-sim` × 3 (`ghcr.io/llm-d/llm-d-inference-sim:v0.8.0`) | `epp-mock` shell — picker + ext_proc loop |
| `llm-d-kv-cache` Indexer + KVEvents pool (imported into `epp-mock`) | |

## Run

```
make 02-up        # podman compose up --build -d
make 02-test      # warm-up + same-prefix + unique-prefix
make 02-down      # podman compose down
```

(All three are thin wrappers over `scripts/up.sh` / `test.sh` / `down.sh`.)

`test.sh` runs warm-up → 5 same-prefix → 5 unique-prefix and prints which pod served each request. Same-prefix should converge to one pod; unique-prefix rotates.

- `localhost:8080` — Envoy ingress
- `localhost:16686` — Jaeger UI

## Trace shape

```
envoy: ingress
  ├─ ext_proc → bbr-mock
  ├─ ext_proc → epp-mock → epp.pick
  │   └─ llm_d.kv_cache.score_tokens
  └─ upstream → vllm-sim-N
```

## Hash alignment

Sim and EPP must agree on block size, hash seed, and tokenizer. Both ends use `kvblock.ChunkedTokenDatabase` with `--block-size=16 --hash-seed=42`; both tokenize via the sim's regex tokenizer (mirrored in `epp-mock/epp.go`). Real HF tokenizers via a shared UDS service land in 03.

## Strategies

```
STRATEGY=round-robin podman compose up --build -d   # or tenant-hash | model-hash | random | prefix-aware (default)
```

When `STRATEGY != prefix-aware`, the indexer + ZMQ subscribers don't start.
