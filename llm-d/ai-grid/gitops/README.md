# AI Grid multicluster lab

This directory is the canonical configuration for the Dagobah hub and site-d provider.
Start with [the topology](topology.yaml), choose a site, then edit its configuration.
This is an experimental ODH integration lab, not a supported RHOAI release.

[Object ownership](docs/object-ownership.md) identifies admin inputs, generated resources,
certificate issuers and lifecycle owners. Grid enrollment and network bootstrap remain
incomplete; running model Pods do not imply an operational multicluster Grid.

| Directory | What belongs here | Start here |
|---|---|---|
| `sites/` | Cluster identity, model placement, gateway configuration and site-specific inputs | [Site inventory](sites/README.md) |
| `models/` | Model definitions and runtime profiles reused by site deployments | [Model catalog](models/README.md) |
| `charts/` | Shared Helm templates for bootstrap, Applications and local models | [Chart usage](charts/README.md) |
| `observability/` | ACM collection, dashboards, alerts and shared logging | [Observability](observability/README.md) |
| `payload/` | Source and image pins, custom patch sources, outstanding merges | [Payload inventory](payload/README.md) |
| `docs/` | Architecture, operations and dated lab notes | [Documentation index](docs/README.md) |
| `tests/` | Runnable acceptance probes and their inputs | [Validation](tests/README.md) |
| `evidence/` | Recorded results and historical inputs; never an Argo source | [Evidence guide](evidence/README.md) |

The two local Qwen instances are deployed from one model definition and two placements
in `sites/dagobah/site.yaml`. ODH Model Controller creates their InferenceProviders.
The existing remote Qwen instance is recorded under site-d and is still manually managed.
An ACM-imported cluster is not automatically a Grid-enrolled site.

## Common changes

- Move or add a local instance: `sites/dagobah/site.yaml`, under `localServing.instances`.
- Change model revision or runtime arguments: `models/qwen3-coder-30b-a3b/dagobah.yaml`.
- Change the hub gateway: `sites/dagobah/gateway.yaml`.
- Review unmerged or custom payload content: `payload/customizations.yaml`.
- Add a site: create `sites/<name>/`, then add its entry to `topology.yaml`; use
  [site ownership and enrollment guidance](sites/README.md).

Work on `main`. Validate the rendered resources, commit with GPG signing and DCO sign-off,
then push directly to `main`. Sync the affected Argo Applications; model and gateway sync
is manual during qualification. [Operations](docs/operations.md) lists namespaces and
[the gateway workflow](docs/gateway-configuration.md) covers configuration and rollback.

The earlier `argo-acm`, `v2`, `v3` and top-level demo packages are historical experiments.
Their former deployment roots are retired. Current logging and ACM observability inputs
live here under `observability/`; the old renderer must not regenerate them.
