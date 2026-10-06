# AWS grid: three single-node clusters

Built and torn down 2026-10-06. Everything here produced a converged grid from empty
namespaces, so it is the reproducible path rather than a record of one.

- `install-grid.sh` — CRDs, three operators, then seeds and signals addresses taken from
  the Services' real ingress hostnames. Re-runnable.
- `grid-invite-policy.yaml` — one ACM Policy per site, delivering the invite token, the CA
  bundle and the gossip key from the hub into each site's operator namespace. Compliant in
  about 20s, and nothing is copied by hand.

## What the clusters were

Three SNO clusters, OpenShift 4.22.15, `m6i.8xlarge`, one per AZ in us-west-2 b/c/d,
`publish: External` on `aws.hexfusion.io`. Terraform module on the experiments branch
`aws-grid-buildout`. About $5-6 an hour for the three.

## What it needs that is not obvious

Pin each cluster to one AZ: the three-zone default wants twelve elastic IPs for three
clusters against a regional default of sixteen.

CRDs go in before the release and the release runs with `crds.enabled=false`, because the
chart renders CRDs beside the resources that use them and Helm validates the set first
(grid#303).

Seeds need each peer's **ingress** address, which on AWS is its SWIM Service load balancer,
not the NAT address a source range needs (grid#302).

The operator image here predates the signals Service split, so each site advertises
`signals.advertiseAddress` explicitly. An image built from grid#300 would not need it.

An ACM `ManagedClusterSetBinding` must be named for the set it binds, the managed clusters
need the `name` label the Placement selects on, and ACM's import does not set it. A policy
bound to nothing reports nothing wrong.

Allow each cluster's NAT address on the hub's **443** as well as 6443: enrollment is a
Route, and the sites dial it from their egress addresses.

After an install, wait two minutes before reading `oc get gridsite`. A fresh load balancer
hostname takes that long to resolve and the operator retries visibly while it does.
