# site-d

A peer inference site on the LAN, reachable from another cluster.

The point is a real network boundary. Two tenants on one cluster exercise
MaaS tenancy but not grid: there is no boundary to cross, no peer identity
to establish, and no independent failure domain. site-d runs on a
different machine from the site calling it, so the hop is real and the
certificate that authorises it has to be too.

## Run it

```bash
./up.sh                  # real inference on CPU
BACKEND=sim ./up.sh      # the llm-d simulator, starts in seconds
./down.sh
```

`up.sh` is idempotent. It reuses the cluster if one exists and reports
which firewall ports still need opening, since that step needs an
authenticator and cannot be scripted unattended.

## Why CPU

A provider site has to serve requests, expose metrics, and present a
certificate. None of that needs a GPU, and a slower site makes the routing
demonstration better rather than worse: the load difference between sites
is real, so the decision grid makes is real.

`BACKEND=sim` swaps in the llm-d simulator, which mirrors vLLM's
Prometheus metrics including KV cache. Useful when you want the load
signal to be reproducible on cue rather than dependent on how a CPU
happens to be scheduled.

## Ports

Bound on every interface via kind `extraPortMappings`, so peers reach
them at the host's LAN address.

| Port | Purpose |
|------|---------|
| 30080 | OpenAI-compatible model endpoint and `/metrics` |
| 30443 | provider gateway, mTLS, reserved |
| 30091 | signals endpoint scraped by a peer's grid operator |

## Direction

The consumer initiates. A site behind NAT can call out to a peer and
scrape its signals, but cannot be called back. That is enough for routing
and for signal collection; bidirectional gossip is not.
