# Spoke-and-Hub: multi-cluster routing

A **hub** picks the **cluster**, each **spoke** picks the **pod**. Built on stock llm-d (one IPP code change).
No upstream multi-cluster guide exists; this is that scenario.

```
client -> hub Gateway -> [IPP-pre: MODEL] -> hub EPP: pick CLUSTER
       -> [IPP-post: pool credential] -> spoke Gateway -> spoke EPP: pick POD -> vLLM
```

## Scope

- **Multi-pool per cluster** - spoke demuxes `x-model` (`200 / 200 / 404`).
- **Hub consumes multiple per-pool metrics** from a manual list, isolated per pool.
- **`file-discovery` on stock v0.9.0** - ConfigMap endpoints, no proxy, no code.
- **Spoke gateway per-pool `/metrics`** - `gw:8080/metrics/<pool>` (validated under load).
- **Proxy-free e2e, per-pool** - `x-model` -> per-model file-discovery EPP -> ORIGINAL_DST -> spoke -> pod,
  **HTTP 200** for both model-a and model-b, no proxy. Config inventory + unwind: [`manifests/hub/FILE-DISCOVERY.md`](manifests/hub/FILE-DISCOVERY.md).

## Benchmark results

3-cluster, controlled (unloaded vs spoke-a heated), llm-d hub EPP vs round-robin, single k6 run.

**Affinity** - sessions stick: llm-d **100%** vs round-robin 35%.

![affinity](images/affinity.png)

**Load shed (heated)** - traffic still hitting the loaded cluster, by hub picker:

| picker | loaded cluster | p50 |
|---|---|---|
| round-robin (baseline) | 23% | 521ms |
| max-score (default) | **0%** | 573ms |
| weighted-random | 19% | **509ms** |

Control: unloaded, the hub spreads evenly (a/b/c ~ 35/34/31%), so the shed is real, not a tie-break.
max-score fully vacates the hot cluster (decisive, trades latency); weighted-random sheds softer and wins latency.


## Test

```bash
cluster-setup/up.sh                              # substrate
scripts/1-spokes.sh && scripts/2-hub.sh          # spokes + hub
scripts/3-auth.sh && scripts/4-ipp.sh            # auth (optional)
MODE=load     LOADED=1 benchmark/run.sh          # load-shed: traffic to the loaded cluster
MODE=affinity          benchmark/run.sh          # session stickiness
```
