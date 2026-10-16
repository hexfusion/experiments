# Payload

The payload is the exact software build inventory for this lab: component versions,
container image digests, source revisions and custom patches. It answers what differs
from the standard ODH artifacts and what must merge or ship before those differences
can be removed. Deployment configuration lives in `sites/`, `models/` and `charts/`.

| File or directory | Meaning |
|---|---|
| [customizations.yaml](customizations.yaml) | Current deployed carries, required next-payload PRs, merged changes awaiting artifacts, and lab-only settings |
| [selection.yaml](selection.yaml) | Component source selection, deployment records and acceptance gates |
| [images.yaml](images.yaml) | Full image digests, base provenance, build recipes and verification evidence |
| `patches/` | Source patches and image build recipes for custom artifacts |

Paths in these YAML inventories are relative to the canonical `gitops/` directory.
A source merge does not retire a carry: the published artifact must include it, replace
the custom artifact, and pass acceptance. A pending PR in the inventory is not deployed.

The next custom Grid gateway includes [#391](https://github.com/praxis-proxy/grid/pull/391),
pinned at `3fe45929261afdba730a24ae2c77f0a9a51b2450`, for configurable prefix-affinity
capacity and TTL. It also needs the health-listener migration for the merged Praxis
update. The current canary still runs the older ODH gateway image.

The KServe readiness proposal is excluded pending clarification of the API contract.
See [the outstanding diff](../docs/payload-diff.md) for the human-readable merge list.
