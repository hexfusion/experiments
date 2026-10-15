# AI Grid GitOps

Shared GitOps templates and rollout guidance for AI Grid. Each cluster is a values
file; Dagobah is the first target. Prefer published ODH images pinned by digest and
carry custom builds only for the patches recorded in the shared payload.
This is an experimental approximation of the RHOAI 3.6 architecture, not a supported
RHOAI release or a supported in-place RHOAI-to-ODH upgrade procedure.

| Path | Purpose |
|---|---|
| `bootstrap/` | Shared Helm templates for the Argo project and root Application |
| `apps/` | Shared child Application chart; initially renders no resources |
| `values/lab.yaml` | Dagobah-specific values for both charts |
| `docs/image-inventory.md`, `image-inventory.yaml` | Maintained image registry/source map and verified candidate digests |
| `payload.yaml` | Shared source revisions, patch decisions, and artifact selection |
| `docs/dagobah.md` | Observed baseline and cluster migration notes |
| `docs/acm-gitops-flow.md` | Portal actions, ACM spokes, Helm values and GitOps |
| `docs/rollout.md` | Rollout and rollback procedure |
| `docs/downstream-installation.md` | Draft reusable downstream installation guidance |
| `docs/payload.md` | How to carry, verify, and retire custom patches |

The root tracks `main` and renders `apps/` with the file selected by
`source.valuesFile` (relative to `apps/`). Add environments by providing a values
file and rendering the same bootstrap chart. Shared
Application templates belong in `apps/templates/`, not in per-cluster directories.

The existing `llm-d/ai-grid/argo-acm/` tree remains the live workload source until
each Application is explicitly migrated. Do not point that existing root at this
empty chart or prune its children. The new root has a distinct name, manual sync,
and no cascading deletion finalizer.

See [Dagobah](docs/dagobah.md) before bootstrapping. Payload metadata and documentation
are outside the Application chart templates and are not applied by Argo.
