# grid-signals

Two clusters. Each runs a Grid operator that scrapes its own llm-d pool and polls the other site for theirs.

The point is to show two things. A routing decision can be made from a peer's signals, and a reader can tell a value that is merely old from a collector that has stopped working.

## Components

| Real | Pinned or generated |
|---|---|
| Grid operator, with peer polling, retries and reachability | operator image, built from the branch |
| llm-d router endpoint picker v0.9.0, two of them | |
| vllm-vcr model servers | |
| SWIM membership over MetalLB LoadBalancers | |
| Prometheus, Grafana, and the alert rules | shipped in this directory |

Nothing here mocks the signals path. The operator image carries the shipped code, and the demo reads the endpoint a peer in another cluster reads.

## Run

```
GRID_REPO=~/projects/praxis-proxy/grid ./run.sh
```

The branch is feat/signals on hexfusion/grid. The operator image defaults to the quay build named in the run script, and OPERATOR_IMAGE overrides it. Setting KEEP leaves the clusters up.

The run script rewrites two image fields in the repo's own topology into a generated copy. Forking five hundred otherwise identical lines would leave the rest drifting out of step with the repo it came from.

## What Proof 5 asserts

Proofs 1 to 4 already read the endpoint picker directly. Proof 5 reads what the operator republishes, which is the only path that carries a site and a provider on every sample.

1. Every sample names the site and provider it came from, and when it was taken. Without the labels a consumer cannot join a sample to a routing candidate. Without the timestamp it cannot tell a fresh value from one the publisher has been holding since its collector broke.
2. Each site holds the other's signals, which only a peer poll can have put there.
3. A site that cannot reach its peer says so, and keeps serving what it already has.

## Staging a partition

```
./chaos.sh cut pool-b       # pool-b's signals stop resolving
./chaos.sh status
./chaos.sh heal pool-b
```

The grid repo's lost-peer verification kills the operator, and notes in its own doc comment that this is not real network-level isolation. A killed operator stops answering and stops polling, so its peer sees a member that left. Withdrawing one TCP port from the SWIM Service is different: SWIM is UDP, so membership still says the site is alive while its signals stop being reachable. Four things then have to hold at once, and Proof 5 checks all four:

- the reachability gauge for that peer drops to zero
- the last value is still served, because it is the best available and dropping it would leave a candidate with no score
- its timestamp stops advancing
- everything recovers when the port returns

## Observability

This directory ships a Prometheus scrape config, nine alert rules, and a Grafana dashboard. The rules cover three states that call for different responses: a signal that is old, a collector that cannot reach a peer, and a peer that never answered. Every rule is a subtraction a reader does against its own clock, which is the argument for putting the timestamp on the sample rather than shipping an age alongside it.

The dashboard pairs each fact with the delayed copy of it that a routing decision would run on. On the compose harness this branch used earlier, one sample during a load surge read:

```
east queue depth        source vs each observer
  SOURCE (pool-east)     108.62
  east   (own collector) 103.50   age 1.05s
  west                    87.88   age 2.59s
```

Nothing is broken there. The peers hold a correct measurement taken two seconds ago, and the source has been climbing since.

## Measured on three clusters

Convergence, with every operator serving every site:

```
pool-a   serves: pool-a pool-b pool-c
pool-b   serves: pool-a pool-b pool-c
pool-c   serves: pool-a pool-b pool-c
```

Cutting pool-c, then healing it:

```
                    cut          healed
collection_up       0            1
pool-a age          3.9s         0.8s     own scrape
pool-b age         24.8s         1.4s     peer poll, 30s interval
pool-c age         64.2s         1.4s     climbing while unreachable
```

While cut, the other two kept serving pool-c's last reading with its timestamp frozen. The reachability gauge did not latch: it returned to one as soon as the port came back. The failure classified as refused rather than a timeout, because a withdrawn service port declines the connection instead of going silent.
