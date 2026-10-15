# Lab GitOps

The root for the next Dagobah stack. Prefer published ODH images pinned by digest;
carry custom builds only for the patches recorded in the cluster payload.
This is an experimental approximation of the RHOAI 3.6 architecture, not a supported
RHOAI release or a supported in-place RHOAI-to-ODH upgrade procedure.

| Path | Purpose |
|---|---|
| `dagobah/bootstrap/` | Argo project and root Application |
| `dagobah/apps/` | Explicit list of child Applications; initially empty |
| `dagobah/payload.yaml` | Source revisions, patch decisions, and artifact selection |
| `dagobah/README.md` | Observed baseline and cluster migration gates |
| `docs/rollout.md` | Rollout and rollback procedure |
| `docs/downstream-installation.md` | Draft reusable downstream installation guidance |
| `docs/payload.md` | How to carry, verify, and retire custom patches |

The existing `llm-d/ai-grid/argo-acm/` tree remains the live source until each
Application is explicitly migrated. Do not point the existing root at this empty
root or prune its children. The new root has a distinct name, manual sync, and no
cascading deletion finalizer.

See [Dagobah](dagobah/README.md) before bootstrapping. Docs and payload metadata are
outside the Application source path; Argo applies only explicitly listed resources.
