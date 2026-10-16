# Engineering handoff: Grid enrollment, 2026-10-16

## Current state

[AI Gateway operator PR #165](https://github.com/opendatahub-io/ai-gateway-operator/pull/165)
is **draft**, with signed and signed-off commit
`587bdb80e173d155787fcae8777d07f90fec6429` on `hexfusion:feat/grid-enrollment`.
It is source work only: no new payload image was built or deployed to the lab.

[ODH operator PR #4186](https://github.com/opendatahub-io/opendatahub-operator/pull/4186)
remains unchanged on `aigrid-dsc` at
`17b556f299d356aea4bd744df65ee22d054678c4`. The enrollment setting needs the
receiving AI Gateway implementation before DSC projection and payload selection.

Share this repository's [canonical lab directory](../README.md). The repository is
public and its default branch is `main`. The [payload inventory](../payload/README.md)
records deployed and proposed changes separately. This handoff does not constitute
a fresh live-cluster inventory.

## Agreed API

```yaml
spec:
  aiGrid:
    managementState: Managed
    grid-enrollment:
      enabled: true
```

- `grid-enrollment` is optional. Omitted, empty, or disabled retains operator-only behavior.
- Enrollment requires explicit opt-in. Do not infer the enrollment host from ACM or labels.
- AI Gateway operator owns the receiving API and deployment logic.
- ODH operator will expose the field in DataScienceCluster and forward it separately.
- Enabling the enrollment service does not enroll sites or create a complete GridNetwork.
- Initial site bootstrap must preserve existing valid certificates and credentials.
  Missing or inconsistent trust must not silently create a new authority.

## Draft implementation

The module renders a pinned enrollment chart into `ai-grid-enrollment`, with a
bootstrap Job, PostgreSQL, an 8 Gi PVC, enrollment Deployment, Service and RBAC.
The enrollment binary generates certificates; the module does not render secret
bytes. The CA signing key stays outside the Grid operator's applications namespace.

Both image references are required on the module Deployment:

- `RELATED_IMAGE_ODH_PRAXIS_AI_GRID_ENROLLMENT_IMAGE`
- `RELATED_IMAGE_ODH_PRAXIS_AI_GRID_ENROLLMENT_DB_IMAGE`

The service is cluster-internal. CSR audit recording is disabled in this profile.
The chart source is `opendatahub-io/praxis-ai-grid-operator` at
`270fa9255f71c90058c049e8f7f840d2c242f73a`, matching the existing module's Grid pin.
No corresponding new image selection is recorded as deployed.

Readiness requires successful bootstrap and available enrollment/database Deployments.
Disabling enrollment removes its workloads while retaining certificate Secrets, PVC and
namespace. A changed Job specification uses a new name to avoid immutable-template errors.
Completed Jobs remain until removal or an explicit retry/renewal operation.

## Validation

| Check | Result |
|---|---|
| `make lint test` | Passed, including code generation, CRD/RBAC, formatting and vet |
| New Kind enrollment integration test | Passed: optional field, opt-in, failed-bootstrap readiness, disablement and retained state |
| Full integration suite | Failed at the existing release-metadata assertion expecting `AI Gateway Operator` as the first release |
| Real enrollment/bootstrap images | Not qualified by the new integration test; it deliberately uses a pause image |
| Complete E2E and OpenShift behavior | Not run |
| GPG signature and DCO | Local signature verification passed; commit contains Signed-off-by |

The full-suite failure occurred in a test with enrollment disabled. Its reported
release list starts with `llm-d Batch Gateway`. Reproduce on unchanged main before
classifying the failure as a baseline regression.

The PR adds an explicit standard golangci-lint configuration. Previously, this
checkout inherited the developer's home-directory lint configuration and reported
additional findings in unchanged files.

## Resume order

1. Review draft #165 and qualify real enrollment/database images under generated RBAC.
   Exercise first bootstrap, existing certificates, disable/re-enable, image upgrades
   and serving-certificate renewal. Keep it draft until these gaps are resolved.
2. Check cleanup of old bootstrap Jobs on image-only upgrades and add the chart to
   the module's reported source inventory. Confirm OpenShift permissions and storage.
3. Reproduce the release-metadata test failure on unchanged main; complete integration
   and E2E validation. The new test validates controller lifecycle, not certificate issuance.
4. Merge/publish the receiving module, then add the optional DSC field and image forwarding
   to the platform work. Do not change #4186 without resuming that explicitly held step.
5. Complete site certificate bootstrap and GridNetwork ordering before lab rollout.
   Update image pins and inventory only after selecting and qualifying actual artifacts.

## Other tracked work

| Work | Reference | State |
|---|---|---|
| CRD creators, writers and consumers | [Grid #395](https://github.com/praxis-proxy/grid/pull/395) | Draft documentation |
| Certificate ownership and lifecycle | [Grid #396](https://github.com/praxis-proxy/grid/pull/396) | Draft documentation |
| Certificates before GridNetwork | [Grid #397](https://github.com/praxis-proxy/grid/issues/397) | Implementation follow-up |
| Documentation writer briefing | [Writer handoff](docs-writer-handoff-20261016.md) | Existing lab qualification notes |

GridNetwork is currently created by installation configuration, including Helm/GitOps;
the Grid operator reconciles status. Do not document it as controller-created.
The outstanding site bootstrap work must preserve existing valid material, distinguish
an absent network from API failure, and avoid inferring CA authority from first startup.

## Saved continuation state

The receiving module is pushed; clone its PR branch to continue. Local working copy:
`/tmp/ai-gateway-enrollment-20261016`. The experiments main working copy is
`/tmp/experiments-main-20261016`.

The separate upstream site-bootstrap prototype remains uncommitted in
`/tmp/grid-site-bootstrap-20261016`, branch `feat/site-certificate-bootstrap`, based on
`a134b2a846ce53165816a2e9cc9d2a51b7ba1dce`. Its tracked and untracked changes are saved
in [this WIP patch](../evidence/wip/grid-site-bootstrap-20261016.patch).
**Do not apply this patch to a payload.** It is incomplete and untested, including a
declared test module whose source file has not yet been created. It is separate from
AI Gateway PR #165 and is preserved only to avoid losing implementation context.

The isolated Kind test cluster is `aigo-enrollment`, using
`/tmp/aigo-enrollment-kubeconfig`. It contains test fixtures only; it is not the lab.
Diagnostic logs were saved locally in `/tmp/aigo-enrollment-integration.log` and
`/tmp/aigo-enrollment-final-checks.log`. These temporary paths may not persist.
