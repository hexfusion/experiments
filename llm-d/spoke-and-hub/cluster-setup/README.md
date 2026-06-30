# Cluster setup (the substrate)

The guide in [../README.md](../README.md) is **all configuration** - it assumes a 1-hub + 2-spoke topology
already exists and just applies router/auth config onto it. This directory is that substrate: how to get
the three clusters and the gateway data plane, kept separate so the guide stays portable across
environments (kind today; vcluster / real clusters on the lab next).

## What it provisions

| cluster | kind context | role | region |
|---|---|---|---|
| `aig-hub` | `kind-aig-hub` | hub (picks the cluster) | - |
| `aig-spoke-a` | `kind-aig-spoke-a` | Inference Cluster 1 | us |
| `aig-spoke-b` | `kind-aig-spoke-b` | Inference Cluster 2 | eu |

All three share one podman network; MetalLB gives each a LoadBalancer band so the hub can reach each spoke
(scrape its EPP aggregate + forward inference). istio is the ext_proc data plane and is installed per phase
where it is needed (`1-spokes.sh` on the spokes, `2-hub.sh` on the hub), not by the substrate.

## kind (default - zero cost, no GPUs)

```bash
./up.sh     # 3 kind clusters + MetalLB bands + CoreDNS fix
# ... now run the guide (../README.md): 1-spokes -> 2-hub -> 3-auth -> 4-ipp ...
./down.sh   # tear everything down
```

Prereqs: `kind`, `kubectl`, `podman` (rootless ok), `istioctl`. Two host limits to raise for 4 clusters
(`up.sh` warns if they are low): inotify if a node hangs (`sudo sysctl -w fs.inotify.max_user_instances=512`)
and the kernel keyring quota if pods stick in `ContainerCreating` with "session keyring ... disk quota
exceeded" (`sudo sysctl -w kernel.keys.maxkeys=5000 kernel.keys.maxbytes=2000000`). Other rootless-podman
gotchas (cgroup delegate, single-node, /24 net, DNS pollution) are handled in `../scripts/common.sh` and `up.sh`.

kind proves the routing **logic** end to end. The sim models load->latency deterministically, so the
load-shed latency payoff is measurable here too (it is not a real-GPU-only effect); it just needs the
steady-state picker/load tuning (greedy picker, cool-cluster headroom). The always-stable headline on kind
is the routing *decision* (`pct_loaded` distribution + stickiness). Real GPUs add fidelity, not the signal.

## Lab (real GPUs, multiple clusters)

The lab is one OpenShift cluster, so "multiple clusters" needs one of these. Ranked:

1. **vcluster on dagobah (recommended).** Run 2–3 virtual clusters (each its own API server) inside the
   host cluster; their pods sync to the real nodes, so sim/encoder/decoder land on the actual T4 / A5000
   GPUs. The hub vcluster scrapes the spoke vclusters' EPP aggregate over host MetalLB LB IPs - identical
   wiring to kind, on real silicon. No new hardware, real separate control planes. This is the sweet spot.
2. **dagobah + a single-node cluster on endor = two genuinely separate clusters.** Real network separation
   (closest to production), but it is a cluster stand-up on endor and more moving parts.
3. **kind-on-lab** buys nothing over kind-on-laptop (no GPUs without painful device passthrough), and
   **namespaces-as-clusters** does not prove cross-*cluster* (shared API server + network). Skip both.

Only `up` / `down` are kind-specific. The istio install and everything in the
guide are environment-agnostic: point `kubectl` contexts at the hub/spoke clusters and the same steps apply.
A `vcluster/` bring-up script lands here when the lab tier is built.
