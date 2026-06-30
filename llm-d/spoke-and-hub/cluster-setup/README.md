# Cluster setup (the substrate)

The guide in [../README.md](../README.md) is **all configuration** - it assumes a 1-hub + 3-spoke topology
already exists and just applies router/auth config onto it. This directory is that substrate: how to get
the clusters and the gateway data plane, kept separate so the guide stays portable across environments.

## What it provisions

| cluster | kind context | role | region |
|---|---|---|---|
| `aig-hub` | `kind-aig-hub` | hub (picks the cluster) | - |
| `aig-spoke-a` | `kind-aig-spoke-a` | Inference Cluster 1 | us |
| `aig-spoke-b` | `kind-aig-spoke-b` | Inference Cluster 2 | eu |
| `aig-spoke-c` | `kind-aig-spoke-c` | Inference Cluster 3 | ap |

All four share one podman network; MetalLB gives each a LoadBalancer band so the hub can reach each spoke
(scrape its EPP aggregate + forward inference). istio is the ext_proc data plane and is installed per phase
where it is needed (`1-spokes.sh` on the spokes, `2-hub.sh` on the hub), not by the substrate.

## Run

```bash
./up.sh     # 1 hub + 3 spoke kind clusters + MetalLB bands + CoreDNS fix
# ... now run the guide (../README.md): 1-spokes -> 2-hub -> 3-auth -> 4-ipp ...
./down.sh   # tear everything down
```

Prereqs: `kind`, `kubectl`, `podman` (rootless ok), `istioctl`. Two host limits to raise for four clusters
(`up.sh` warns if they are low): inotify if a node hangs (`sudo sysctl -w fs.inotify.max_user_instances=512`)
and the kernel keyring quota if pods stick in `ContainerCreating` with "session keyring ... disk quota
exceeded" (`sudo sysctl -w kernel.keys.maxkeys=5000 kernel.keys.maxbytes=2000000`). Other rootless-podman
gotchas (cgroup delegate, single-node, /24 net, DNS pollution) are handled in `../scripts/common.sh` and `up.sh`.

kind proves the routing **logic** end to end. The sim models load->latency deterministically, so the
load-shed decision (`pct_loaded` distribution + stickiness) is fully measurable here; it is not a
real-GPU-only effect. Only `up` / `down` are kind-specific - the istio install and everything in the guide
are environment-agnostic: point `kubectl` contexts at the hub/spoke clusters and the same steps apply.
