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

### dagobah / OSSM bring-up (2026-06-11) — full stack UP, serving 200 OK e2e

Gateway = `data-science-gateway-class` (OSSM3 istiod-managed). Own Gateway in
`extproc-bench` → Envoy proxy pod lands in `extproc-bench` (isolated from RHOAI),
ClusterIP only (lab has no LB; `Programmed=False` is cosmetic, `https` listener
Programmed=True). Admin stats via `pilot-agent request GET stats/clusters/config_dump`.

Traps hit, in order:
1. **forge built stale `main`** — forge builds the LOCAL checkout's current branch,
   not `git.branch`. Must `git checkout <feat>` in `~/projects/llm-d/llm-d-router`
   before `forge pipeline build`. Verify image has the flag: `podman run … --help | grep`.
2. **helm `inferenceExtension.flags` is a {name,value} list** — setting the bool flag
   via `--set` breaks the template. Add it via `patch-epp-flag` (one-arg `=` form).
3. **EPP crashloop: `inferencemodelrewrites` RBAC missing** — GIE v1.0.1 chart RBAC
   predates the InferenceModelRewrite controller; its informer never syncs →
   `WaitForCacheSync` blocks → `PoolHasSynced` never true → probes fail. Fix: grant
   `get/list/watch` on `inferencemodelrewrites.inference.networking.x-k8s.io` +
   a generous `startupProbe` (cache sync ~28s).
4. **HTTPRoute backendRef: OSSM resolves InferencePool only under `inference.networking.x-k8s.io`**,
   not `inference.networking.k8s.io` (→ `InvalidKind` → `NC cluster_not_found` 500).
5. **Two-pool requirement** — EPP watches the `k8s.io/v1` pool; the gateway route
   backends an `x-k8s.io/v1alpha2` pool. Need BOTH (same name/selector/extensionRef),
   like KServe creates. After adding the v1alpha2 twin: ResolvedRefs=True, **200 OK**.

### Open for the A1 measurement (not yet run)
- **OSSM ext_proc breaker default = `max_requests: 4294967295` (unlimited)**, not Envoy's
  1024 → overflow won't occur at reachable concurrency; A1 must set the breaker LOW
  (patch the GIE `epp-epp` DestinationRule `connectionPool`, keep `tls: SIMPLE`).
  (The bench's `knobs.yaml` ISTIO_MUTUAL is wrong here — EPP has no sidecar; SIMPLE.)
- **Processing mode (SEND vs request-only) not yet confirmed** — determines whether
  response streams are held (A-mode overflow reproducible) or released (OSSM already
  in B-mode → not susceptible). Decisive for whether A1 shows overflow here at all.
- Load must be **in-cluster** (ClusterIP gateway): k6 Job, not laptop port-forward.
- EPP `/metrics` is **auth-gated** on this deploy; scrape needs a bearer token w/ scope.

---

## RESULTS — measured capacity model (dagobah, 2026-06-11)

**Headline: 100,000 concurrent held mode-A ext_proc streams sustained through ONE Envoy + ONE EPP.** `inflight` gauge = 99,988, held ~1 min, no restarts, no scrape gaps. Both components coasting.

![100k in-flight ext_proc streams (Grafana, EPP `extproc_streams_inflight`)](docs/100k-inflight-grafana.png)

*Grafana `EPP ext_proc streams` dashboard — `llm_d_router_epp_extproc_streams_inflight` climbing to a clean 100k plateau (~17:14–17:15) then draining on rampdown. Single Envoy (12c/20Gi) + single EPP (8c/24Gi).*

### Measured per-stream cost (mode-A, response observation, slow SSE held ~12s)

| Component | @ 100k used | per-stream |
|---|---|---|
| Envoy (gateway) | ~4 cores / 13.7Gi | **~136 KB mem**, ~40 millicore-equiv |
| EPP | ~3 cores / 6.8Gi | **~68 KB mem** |

**Held mode-A streams are MEMORY-bound, not CPU-bound.** CPU is cheap in steady state.

### The CPU-wall correction (important)
An earlier run showed an *undersized* 8-core Envoy "binding" at ~13k while burning 23 cores — that was an **artifact of OOM-thrashing** (connection resets + restart churn on the 1Gi→8Gi-too-small pod), NOT the true cost. A properly-sized gateway that doesn't OOM holds 100k on ~4 cores. The "~180 cores for 100k mode-A" extrapolation was wrong; real steady-state is ~4 cores + ~14Gi.

### Gateway capacity vs sizing
| Gateway | held streams | bind |
|---|---|---|
| default 2c/1Gi | ~8k | memory (1Gi OOM) |
| tuned 8c/8Gi | ~50k | memory (8Gi) |
| tuned 12c/20Gi | **~100k+** | nothing hit (13.7Gi/20Gi, 4/12 cores) |

### Scaling chain (proven end-to-end)
1. Default RHOAI gateway walls at ~8k (1Gi OOM).
2. **Vertical tune works** → `Gateway.spec.infrastructure.parametersRef` → ConfigMap with `deployment:` strategic-merge-patch (Istio 1.26+; we're on 1.26.2). Sets `istio-proxy` resources, survives reconciliation. ConfigMap must be in the Gateway's namespace; container name `istio-proxy`. (Earlier failures — sidecar annotations, direct Deployment patch (SSA-reverted), Sail CR `global.proxy.resources` (reverted+global) — were all wrong mechanisms.)
3. At ~88k the **EPP** became the bind (single EPP, uncapped, restarted holding ~88k goroutines).
4. Gave EPP a memory request (16Gi/24Gi) + node pin → single EPP held 100k on ~7Gi.

### RHOAI product findings
- Default `data-science-gateway-class` gateway is 2c/1Gi and OOMs ~8k concurrent streaming. **Tunable** via `parametersRef` **only if you own the Gateway object** (user-created InferencePool gateways: yes; RHOAI-platform-reconciled gateways: may strip `spec.infrastructure` — verify per-deployment).
- RHOAI `GatewayConfig` CRD has **no** resource/replica field (auth/cert/domain only). GatewayClass has no `parametersRef`. So there is no RHOAI-native gateway-resources knob — the Istio `parametersRef` path is the lever. **RFE candidate.**
- Horizontal replica scaling also works (`kubectl scale` sticks) → N × ~8k for default-sized pods.

### Metrics-pipeline cost
Flat — ~34 series total (1 gauge + ~16 histogram buckets + ~17 code-counter series) **regardless of stream count**. No Prometheus/Grafana/Thanos boost needed; scrapes stayed clean at 100k. Only watch: EPP CPU headroom so `/metrics` (port 9090) answers the 15s scrape under load.

### PR #1603 value, validated
`llm_d_router_epp_extproc_streams_inflight` tracked held-stream concurrency accurately 0→100k, pinpointed each bind (gateway OOM ~8k, EPP ~88k), and is the sizing/HPA signal + early-warning before the memory wall. Summed across the EPP replica set = the fleet's aggregate held-stream load.
