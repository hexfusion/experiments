# two-route-kind

The two-route topology from `design/work/llm-d/gateway-data-contract/ROUTABLE-DECIDER.md`,
running on kind. The decider is a routable backend, not a filter.

```
client -> gateway -> route 1 (no marker)      -> IPP -> EPP (plain HTTP)
IPP    -> gateway -> route 2 (marker present) -> ORIGINAL_DST -> sim
```

EPP runs the branch at `.worktrees/llm-d-router/epp-http-transport`, which opens
a plain-HTTP transport beside ext_proc. IPP calls that door. Nothing in this
cluster speaks ext_proc to anything.

```
./up.sh        # cluster, istio, sims, gateway, routes, IPP
./verify.sh    # five checks including a load arm
```

`REQUESTS` and `CONCURRENCY` override the load arm. One binary serves all three
roles, selected by `MODE`: sim, ipp, load.

## What it demonstrates

**Loop avoidance is match precedence.** Gateway API cannot express "header
absent", so route 2 carries one header match and route 1 carries none. Istio
orders the one-header route first, and the Envoy route dump confirms it. No
negation anywhere.

**Nothing in the data plane runs ext_proc.** The HTTP filter chain is Istio's
own: metadata_exchange, grpc_stats, alpn, fault, cors, stats, router. The string
`ext_proc` appears only in `BootstrapConfigDump`, which is the list of extensions
the binary was compiled with, never in configuration.

**IPP is stateless, so scaling is load balancing.** 20,000 requests at
concurrency 1000, three IPP replicas, no coordination between them:

```
20000 requests, concurrency 1000, 2.9s, 6815 req/s
transport errors : 0
status           : map[200:20000]
latency p50/p99  : 11.9ms / 735ms
served by sim    : sim-a 6667  sim-b 6667  sim-c 6666
decided by ipp   : 6585 / 6601 / 6814
```

Each client request crosses the gateway twice, so the gateway itself handled
roughly 40,000 requests in that window. The p99 is queueing at concurrency 1000
against three single-replica sims, not routing cost.

**A forged marker bypasses the decider.** `verify.sh` sends `x-ipp-processed`
from a client and gets a 200 with no `x-ipp-destination`, meaning policy never
ran. This is a check that asserts the bypass *works*, because the design depends
on stripping that header at ingress and an untested warning is not a control.

## Observability

`manifests/60-observability.yaml` brings up Jaeger and Prometheus. Reach them with
`kubectl -n two-route port-forward svc/jaeger 16686` and `svc/prometheus 9090`.

```
kubectl -n two-route port-forward svc/grafana 3000     # dashboard, anonymous admin
kubectl -n two-route port-forward svc/prometheus 9090
kubectl -n two-route port-forward svc/jaeger 16686
```

Prometheus scrapes EPP, the gateway's Envoy, the sims, and the kubelet's cAdvisor,
so decisions, the load behind them, and what they cost share one timeline. The
provisioned dashboard has latency, decision rate, per-plugin scheduling cost,
CPU and memory by component, and the queue depth the scorer actually reads.

EPP exports traces to Jaeger. Envoy spans do not arrive despite the provider
being correctly attached (`envoy.tracers.opentelemetry` pointing at a HEALTHY
jaeger cluster) and sampling confirmed at 100. Envoy records no tracer stats at
all, so spans are never created rather than failing to export. Unresolved.

## What the topology costs

30,000 requests, 32KB bodies, concurrency 400, 2166 req/s. CPU in cores, memory
in MiB, from cAdvisor:

| component | CPU | memory |
|---|---|---|
| epp | 0.664 | 68 |
| gateway (envoy) | 0.231 | 124 |
| ipp x3 | 0.278 total | 46 each |
| sims x3 | 0.274 total | 13 each |

The decider dominates. EPP alone costs roughly 3x the gateway and 2.4x the entire
IPP tier, which is consistent with scoring being the expensive part of a routing
decision rather than any transport.

That reframes the overhead question. Adding IPP costs about 0.09 cores and 46MiB
per replica, next to a component already spending 0.66 cores. The two-route
topology's real cost is the second gateway traversal in the latency numbers
above, not the process it adds.

Jaeger reads 0.294 cores and 982MiB here, which is an artifact of sampling every
request and should not be read as a production figure.

## Under large bodies

10,000 requests through the full topology, zero errors, all 200:

| body | conc | p50 | p99 | req/s |
|---|---|---|---|---|
| 150B | 1000 | 13.5ms | 728ms | 5690 |
| 8KB | 500 | 30.3ms | 848ms | 2483 |
| 32KB | 500 | 37.0ms | 601ms | 2277 |
| 32KB | 1000 | 75.8ms | 1277ms | 2253 |

Throughput more than halves from 150 bytes to 32KB, which is what a topology
that moves the body across the gateway twice should look like. Distribution stays
even throughout.

See [BENCH.md](BENCH.md) for ext_proc against plain HTTP on the same EPP, which
is a different question and has a crossover.

## Two things that cost time and are worth knowing

**Destinations must be addresses, not names.** `ORIGINAL_DST` reads an address
out of the header and does not resolve DNS. `up.sh` resolves sim pod IPs at
deploy time, which is also what a real picker would name.

**Cluster DNS names do not resolve usefully from a pod here, repeatedly.** Using
`demo-istio.two-route.svc.cluster.local` sent IPP's dispatch somewhere that
answered `405 allow: GET,HEAD`, and the request never reached the gateway at all.
The ClusterIP works. The same problem then sent EPP's trace exporter to
`192.168.8.1:4317` and made Prometheus 404 on the EPP target, so three separate
components had to be moved onto resolved addresses or pod discovery. This is the
DNS-pollution class the spoke-and-hub kind poc scripts around; assume any FQDN in
this environment is suspect.

**EPP schedules, and it is checked rather than assumed.** A 200 with a plausible
body proves nothing, so `verify.sh` reads EPP's own picker counter before and
after the load and asserts it moved by the request count. It does:
`EPP scheduled all of it (picker +5000)`. `x-decided-how: epp` on the response
separately rules out the round-robin fallback.

**And the decision is load-aware, tested directly.** Hold sim-a's queue deep by
loading it, then send gateway traffic:

```
sim-a queued (40), b and c idle : sim-a    0   sim-b 1493  sim-c 1507
skew removed, same traffic      : sim-a 1024  sim-b 1033  sim-c  943
```

EPP routes away from the queued pod completely, and back once it drains. The
control run matters: without it, "sim-a got nothing" only means sim-a was broken.

An earlier version of this README pointed at a 6768/6503/6729 spread as evidence
of load-awareness. That was noise on an unloaded cluster and proved nothing. The
skew test is the evidence.

The sims model a bounded batch plus a queue rather than reporting every in-flight
request as running, because the load-aware scorer reads WaitingQueueSize. A fake
with an always-empty queue leaves it blind, and the first run of this experiment
showed an even split for exactly that reason.

## What it does not do

No prefix scoring. EPP runs load-aware only, because the precise prefix scorer
needs a tokenizer sidecar and KV events from a real engine. The sims publish the
vLLM metric names EPP scrapes so load-aware has something to work with.

No response-phase work, no decision broadcast, no second cluster. The hub-and-spoke
claim is that route 2 points at a remote service instead of a local pool, and that
is not built here.
