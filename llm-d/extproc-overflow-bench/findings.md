# Findings — extproc-overflow-bench

> Stub. Filled after runs complete. Product-shape, lab-free conclusions only
> (per the report-only-the-conclusion rule); raw per-run data lives in `results/`.
> Run `make summarize` to synthesize results/ into the per-arm + formula numbers below.

## Capacity model — measured terms

Formula: `λ_max = W × min(R, K×S) / E[T_hold]`; multiplier `B/A = E[T_response]/E[T_route]`.

| Term | Source-default | Measured (this stack) |
|---|---|---|
| `W` (Envoy workers) | = pod CPU | _pending_ |
| `R` max_requests | 1024 | _pending_ |
| `K` max_connections | 1024 | _pending_ |
| `S` max_concurrent_streams (EPP advertises none ⇒ 1024) | 1024 | _pending_ |
| `E[T_response]` (full SSE) | — | _pending_ |
| `E[T_route]` (decision) | — | _pending_ |
| **predicted** `C_extproc = W×min(R,K×S)` | — | _pending_ |
| **predicted** multiplier `E[T_response]/E[T_route]` | — | _pending_ |

## C1 — ext_proc overflow + "no knob"

- [ ] **A1** default breaker: concurrency at first `*_overflow`, per gateway —
      does it match predicted `C_extproc`?
- [ ] Mechanism confirmed: `upstream_rq_pending_overflow` ↑ and `rq_open=1`
      coincident with 500s, gateway CPU **below** limit? (breaker-bound, not CPU)
- [ ] **A2** breaker wide open: new ceiling + binding resource named
      (Envoy CPU% of limit / FDs / mem, from pod-resources scrape).
- [ ] **B** `response_body_mode: NONE`: max concurrency reached with **zero**
      overflow; measured multiplier vs A1 — does it track `E[T_response]/E[T_route]`?
- [ ] Verdict on "no config knob" / "replace Envoy": _pending_.

## C2 — serialization / TTFT

- [ ] JSON parse+marshal cost at 220KB (expect ~17ms): _pending_.
- [ ] ext_proc body-stream + queue share of client TTFT: _pending_.
- [ ] Throughput plateau location + queue-shape confirmation: _pending_.
- [ ] Verdict on "serialization causes >1s TTFT": _pending_.

## Bugs / gotchas surfaced

_(per house style — record env collisions, CRD quirks, gateway-specific traps)_
