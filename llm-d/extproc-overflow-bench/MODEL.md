# Capacity Model — derivation


The lab finds the ceiling on *this* stack; the formula explains the ceiling on
*any* stack. The experiment exists to validate each term, not to produce a single
number. Every symbol is anchored to Envoy source (`envoyproxy/envoy` @ `f71b6882`).

**ext_proc stream-pool capacity** — the most concurrent ext_proc streams Envoy
will carry to the EPP cluster:

```
C_extproc  =  W × min( R , K × S )

  W = Envoy worker threads (--concurrency)        per-thread, pools are thread-local
                                                  (#39281 "not shared across threads")
  R = circuit_breakers.max_requests   (def 1024)  upstream_impl.cc:2124; gate at
                                                  conn_pool_base.cc:242
  K = circuit_breakers.max_connections(def 1024)  upstream_impl.cc:2122
  S = max_concurrent_streams per conn (def 1024)  protocol.proto:598, capped by the
                                                  EPP gRPC server's advertised
                                                  SETTINGS_MAX_CONCURRENT_STREAMS
                                                  (codec_impl.cc:242) — EPP sets none
                                                  (runserver.go:194) ⇒ S = 1024
```

**How many streams are actually held** — Little's Law over the in-flight set:

```
L_held  =  λ × E[T_hold]        λ = request arrival rate
                                E[T_hold] = mean time a request holds its ext_proc stream
```

`overflow` (HTTP 500) occurs exactly when `L_held > C_extproc`. So the maximum
sustainable arrival rate before ext_proc saturates is:

```
λ_max  =  C_extproc / E[T_hold]  =  W × min(R, K×S) / E[T_hold]
```

**The whole argument is in what sets `E[T_hold]`, and that is a processing-mode
choice, not an Envoy limit:**

| Mode | What holds the stream | E[T_hold] | Consequence |
|---|---|---|---|
| **A** `response_body_mode: SEND` (default) | the entire SSE response (`ext_proc.cc:991-1010`) | **E[T_response]** = TTFT + N_tokens × ITL — *seconds to minutes* | ext_proc is the bottleneck; max concurrent requests = `C_extproc` |
| **B** `response_body_mode: NONE` | only the routing decision (`ext_proc.cc:1385`) | **E[T_route]** = body parse + pick — *~tens of ms* | streams released immediately; ext_proc drops out of the binding constraint |

**The capacity multiplier from closing the stream:**

```
λ_max^B / λ_max^A  =  E[T_response] / E[T_route]
```

For a 2000-token answer at ~30 tok/s (≈67 s response) vs a ~17 ms routing
decision, that ratio is **~10³–10⁴×**. This is why mode A "saturates quickly"
(the #1582 symptom): the pool fills with *responses*, not requests, and response
duration dwarfs decision time. Mode B doesn't raise the breaker — it removes
`E[T_response]` from the equation, so the same Envoy carries orders of magnitude
more concurrency with no knob change.

**Where the system max actually lands per mode:**
- **A1** (default breaker): max concurrent requests = `C_extproc = W × 1024`.
- **A2** (breaker opened wide): `C_extproc → ∞`, so the binding term becomes Envoy
  CPU / FDs / gateway pod memory — the true held-path hardware ceiling.
- **B**: ext_proc no longer binds; ceiling is downstream (vLLM/sim) throughput and
  Envoy connection/CPU — the system runs as if EPP routing were free.

### Downstream cluster (the inference path) — same formula, different numbers

The model above is the ext_proc sidestream (gateway → EPP). The **downstream**
backend cluster (gateway → vLLM) obeys the identical shape with the backend's
breaker values:

```
C_backend   = W × min( R_be , K_be × S_be )
C_inference = min( C_backend , N_vllm )        N_vllm = vLLM concurrent seqs (max_num_seqs, KV-block bound)
λ_be        = N_vllm / E[T_response]
```

Two differences downstream: (1) vLLM serves **HTTP/1.1** → no multiplexing →
`S_be = 1`, so `C_backend = W × min(R_be, K_be)`; (2) the breaker is **admission
control in front of `N_vllm`**, not the real ceiling — `C_inference` is. In
**mode B** the ext_proc cluster stops binding, so the gateway's ceiling *becomes*
`C_inference`: the system runs as if EPP routing were free.

**The formula is gateway-independent — all three program the same Envoy fields.**
Istio is just one CRD surface (confirmed `istio/api` `networking/v1alpha3/destination_rule.proto`):

| Formula term (Envoy field) | Envoy Gateway | kgateway | Istio `DestinationRule.connectionPool` |
|---|---|---|---|
| `R` = `max_requests` | `BackendTrafficPolicy.circuitBreaker.maxParallelRequests` | `BackendConfigPolicy` | `http.http2MaxRequests` (L679) |
| `K` = `max_connections` | `BackendTrafficPolicy.circuitBreaker.maxConnections` | `BackendConfigPolicy` | `tcp.maxConnections` (L634) |
| `S` = `max_concurrent_streams` | cluster http2 opts | `http2ProtocolOptions.maxConcurrentStreams` | `http.maxConcurrentStreams` (L720) |
| `max_pending_requests` | `circuitBreaker.maxPendingRequests` | `BackendConfigPolicy` | `http.http1MaxPendingRequests` (L675) |

Same `C = W × min(R, K×S)` for both clusters and all three gateways; only the CRD
field names and which cluster you target differ.

**Validation map — which run measures which term:**

| Term | Measured by |
|---|---|
| `W`, `R`, `K`, `S` | gateway config + Envoy `/config_dump`, `/stats` (and EPP `SETTINGS` frame) |
| `E[T_response]`, `E[T_route]` | guidellm (Arm 2) + k6 `ttft_ms`; route time from EPP Prometheus |
| `C_extproc`, `λ_max^A` | Arm 1 A1 — concurrency at first `*_overflow` |
| held-path hardware ceiling | Arm 1 A2 — concurrency at Envoy CPU/FD saturation |
| `λ_max^B` and the multiplier | Arm 1 B — concurrency reached with no overflow; ratio vs A |

