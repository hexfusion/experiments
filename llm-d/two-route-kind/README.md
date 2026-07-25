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

## Two things that cost time and are worth knowing

**Destinations must be addresses, not names.** `ORIGINAL_DST` reads an address
out of the header and does not resolve DNS. `up.sh` resolves sim pod IPs at
deploy time, which is also what a real picker would name.

**The gateway's DNS name did not resolve usefully from a pod here.** Using
`demo-istio.two-route.svc.cluster.local` sent IPP's dispatch somewhere that
answered `405 allow: GET,HEAD`, and the request never reached the gateway at all.
The ClusterIP works. This is the same DNS-pollution class of problem the
spoke-and-hub kind poc scripts around, so `up.sh` resolves the ClusterIP too.

**EPP schedules, and it is checked rather than assumed.** A 200 with a plausible
body proves nothing, so `verify.sh` reads EPP's own picker counter before and
after the load and asserts it moved by the request count. It does:
`EPP scheduled all of it (picker +5000)`. `x-decided-how: epp` on the response
separately rules out the round-robin fallback.

The distribution under EPP is uneven where round-robin was exact thirds, which is
what load-aware scoring reacting to real load looks like:

```
round-robin : 6667 / 6667 / 6666
EPP         : 6768 / 6503 / 6729
```

## What it does not do

No prefix scoring. EPP runs load-aware only, because the precise prefix scorer
needs a tokenizer sidecar and KV events from a real engine. The sims publish the
vLLM metric names EPP scrapes so load-aware has something to work with.

No response-phase work, no decision broadcast, no second cluster. The hub-and-spoke
claim is that route 2 points at a remote service instead of a local pool, and that
is not built here.
