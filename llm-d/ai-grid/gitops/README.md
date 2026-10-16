# AI Grid GitOps

Shared GitOps templates and rollout guidance for AI Grid. Each cluster is a values
file; Dagobah is the first target. Prefer published ODH images pinned by digest and
carry custom builds only for the patches recorded in the shared payload.
This is an experimental approximation of the RHOAI 3.6 architecture, not a supported
RHOAI release or a supported in-place RHOAI-to-ODH upgrade procedure.

| Path | Purpose |
|---|---|
| `bootstrap/` | Shared Helm templates for the Argo project and root Application |
| `apps/` | Shared child Application chart; local-model child enabled in lab values |
| `values/lab.yaml` | Dagobah-specific values for both charts |
| `docs/image-inventory.md`, `image-inventory.yaml` | Maintained image registry/source map and verified candidate digests |
| `payload.yaml` | Shared source revisions, patch decisions, and artifact selection |
| `docs/dagobah.md` | Clean baseline and cluster rollout notes |
| `docs/acm-gitops-flow.md` | Portal actions, ACM spokes, Helm values and GitOps |
| `docs/aigrid-enablement-20261015.md` | First DSC aiGrid enablement result, payload gaps and tested digests |
| `docs/clean-slate-20261015.md` | Authorized reset scope, verification and retained infrastructure |
| `docs/rollout.md` | Rollout and rollback procedure |
| `docs/downstream-installation.md` | Draft reusable downstream installation guidance |
| `docs/carry-patches.md` | Required carries, ODH sync dependencies and remaining integration gaps |
| `docs/auth-flow.md` | MaaS-fronted Grid trust boundaries and request walkthrough |
| `docs/payload.md` | How to carry, verify, and retire custom patches |

The root tracks `main` and renders `apps/` with the file selected by
`source.valuesFile` (relative to `apps/`). Add environments by providing a values
file and rendering the same bootstrap chart. Shared
Application templates belong in `apps/templates/`, not in per-cluster directories.

The old RHOAI/MaaS/Grid stack and its Argo root were removed on 2026-10-15 UTC.
The `llm-d/ai-grid/argo-acm/` tree still supplies shared logging and observability;
its former stack manifests are historical. Do not reapply the retired `ai-grid`
root. The new root has manual sync and no cascading deletion finalizer.

See [Dagobah](docs/dagobah.md) before bootstrapping. Payload metadata and documentation
are outside the Application chart templates and are not applied by Argo.

Manual runtime evidence: [MaaS/Grid inference and integration gaps](docs/maas-grid-integration-20261015.md),
with an [isolated MaaS/Praxis reproducer](tests/maas-extproc/README.md).

Architecture views: [model placement](docs/diagrams/layered/01-model-placement.svg),
[networking close-up](docs/diagrams/layered/02-networking-closeup.svg), and a
[two-view browser page](docs/diagrams/layered/index.html). These show the target
topology with illustrative model names; they do not claim live qualification.

Local serving: [two GitOps-managed Qwen services](docs/local-qwen-serving-20261015.md).
Documentation: [Friday handoff and verified publication evidence](docs/docs-writer-handoff-20261016.md).
