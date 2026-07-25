# ext_proc vs plain HTTP, same EPP

One EPP, one scheduler, one body. The only thing that varies is how the caller
asks for the decision: an ext_proc bidirectional gRPC stream on 9002, or a POST
on 9100. Both arms run twice and the better run of each is reported, because
ordering effects on a laptop are larger than the difference being measured.

3000 requests per arm. Ratios are http relative to ext_proc, so below 1.0 means
http is faster and above 1.0 means http is slower.

| body | conc | http p50 | ext_proc p50 | p50 | http p99 | ext_proc p99 | p99 | http rps | ext_proc rps | tput |
|---|---|---|---|---|---|---|---|---|---|---|
| 150B | 50 | 1.68ms | 1.81ms | 0.93x | 7.35ms | 3.96ms | 1.86x | 23796 | 26629 | 0.89x |
| 8KB | 50 | 2.82ms | 2.32ms | 1.21x | 10.27ms | 5.24ms | 1.96x | 16172 | 20143 | 0.80x |
| 16KB | 50 | 3.25ms | 3.08ms | 1.06x | 9.67ms | 6.68ms | 1.45x | 13527 | 15585 | 0.87x |
| 32KB | 50 | 4.35ms | 7.22ms | 0.60x | 21.04ms | 17.23ms | 1.22x | 9890 | 6620 | 1.49x |
| 64KB | 50 | 5.88ms | 9.47ms | 0.62x | 21.78ms | 21.99ms | 0.99x | 7198 | 5087 | 1.42x |
| 128KB | 50 | 9.74ms | 15.33ms | 0.64x | 34.57ms | 28.31ms | 1.22x | 4188 | 3191 | 1.31x |
| 64KB | 200 | 13.78ms | 36.53ms | 0.38x | 126.94ms | 77.32ms | 1.64x | 7940 | 5065 | 1.57x |
| 64KB | 500 | 35.52ms | 96.95ms | 0.37x | 246.72ms | 183.91ms | 1.34x | 8438 | 4821 | 1.75x |

Large bodies, which is where multi-turn sessions actually live. 400 requests,
concurrency 20:

| body | http p50 | ext_proc p50 | p50 | http p99 | ext_proc p99 | p99 | http rps | ext_proc rps | tput |
|---|---|---|---|---|---|---|---|---|---|
| 546KB | 16.54ms | 20.48ms | 0.81x | 23.76ms | 44.11ms | 0.54x | 1195 | 920 | 1.30x |
| 2.1MB | 59.26ms | 121.12ms | 0.76x | 121.12ms | 128.95ms | 0.94x | 317 | 248 | 1.28x |
| 6.4MB | 183.66ms | fails | n/a | 305.52ms | fails | n/a | 105 | 0 | n/a |

The cliff, bracketed at 100 requests and concurrency 10:

| body bytes | ext_proc | http |
|---|---|---|
| 3,713,540 | 100 decided | 100 decided, 1.19x throughput |
| 4,150,540 | 100 decided | 100 decided, 1.15x throughput |
| 4,259,790 | **0 decided, 100 errors** | 100 decided |

The boundary is gRPC's 4 MiB default, 4,194,304 bytes. The failing case reports
`4259802 vs. 4194304`, so the ProcessingRequest wrapper costs about 12 bytes on
top of the body. Nothing degrades on the way in: it works, then it does not.

Zero errors in every run, and all 3000 decided in every run, so both arms are
doing the same work.

## What it says

**The crossover sits around 16 to 32KB.** Below it ext_proc wins on median and
throughput. Above it HTTP wins both, and the margin grows: 1.42x throughput at
64KB, 1.75x at 64KB with concurrency 500.

**ext_proc owns the tail almost everywhere.** p99 is 1.2x to 2x better for
ext_proc at every size except 64KB at concurrency 50, where they tie. grpc-go's
HTTP/2 stack is genuinely good at head-of-line behaviour under load, and a naive
`net/http` client is not.

**Concurrency widens the median gap and does not fix the tail.** At 64KB the
median ratio goes 0.62x, 0.38x, 0.37x as concurrency rises 50, 200, 500, while
p99 stays worse for HTTP throughout.

**ext_proc stops working entirely at 4MB.** At 6.4MB every request fails with
`ResourceExhausted: grpc: received message larger than max (6711372 vs.
4194304)`, while HTTP serves all 400. That is gRPC's default receive limit.

It is a default rather than a wall: EPP exposes `--grpc-max-recv-msg-size`, and
Envoy has its own equivalent. But both ends have to agree on the new number, the
buffer is per message, so memory scales with concurrency times body size, and the
out-of-the-box behaviour is a hard failure at a size real multi-turn sessions
reach. HTTP has no per-message ceiling to negotiate.

Neither transport is better outright below 4MB. HTTP suits large multi-turn
bodies and does not suit a latency-sensitive stream of small requests, which is
roughly the agentic shape. Above 4MB the comparison stops being about latency.

## What this does not measure

The two-route topology. That adds a second gateway traversal and a process, and
is a separate and larger cost than anything here.

Connection strategy is not held constant, and that is deliberate because it
reflects the real deployment. grpc-go multiplexes every stream over one
connection; the HTTP client keeps a pool. With a single EPP replica that costs
ext_proc nothing, but it is also the reason a real multi-replica ext_proc
deployment pins to one pod without `dns:///` and round_robin.

Response phase. Request phase only, so nothing here reflects usage accounting.

## Reproducing

```
MODE=bench EPP_GRPC=<epp>:9002 EPP_HTTP=http://<epp>:9100 \
  REQUESTS=3000 CONCURRENCY=50 BODY_KB=64 ./two-route
```
