# Custom payload carry assessment

Checked 2026-10-15. These are source and integration decisions, not deployed
artifacts. [payload.yaml](../payload.yaml) remains assembling with no selected
artifacts; [the image inventory](image-inventory.md) retains the ODH base digests.

## Required changes

| Change | Pinned revision | Delivery / reason |
|---|---|---|
| [Platform #4186](https://github.com/opendatahub-io/opendatahub-operator/pull/4186) | `f1d5a3e561825f4343bd9b27c9582b503f3d716a` | Carry: DSC aiGrid enablement and related-image forwarding. Build/select a consistent operator, embedded manifests, bundle and catalog. |
| [Chart #181](https://github.com/opendatahub-io/odh-gitops/pull/181) | `b226467346668d030feb4981b55b526d47432f6c` | Carry: gateway/enrollment runtimes with the platform owning the Grid operator. Chart package and values must match the selected runtime. |
| [Model controller #966](https://github.com/opendatahub-io/odh-model-controller/pull/966) | `3d892d76afeb92d59d0bdec7baf0dc0892081f79` | Carry for automatic provider publishing and trusted Grid fairness/objective headers. Include the matching RBAC and administrator-owned credential configuration. |
| [Grid SiteInvite #382](https://github.com/praxis-proxy/grid/pull/382) | upstream merge `ac81a4e594af08a4750b7645b8948b3952a5c606` | Required for declarative enrollment. [ODH sync #35](https://github.com/opendatahub-io/praxis-ai-grid-operator/pull/35) is open. Prefer its published ODH image after merge; carry only until that is available and verified. |

The SiteInvite change needs more than an enrollment image: refresh the SiteInvite
CRD through its platform owner and the enrollment chart's RBAC/environment. The
current chart #181 vendors Grid `3359afdc`, and the inspected ODH Grid candidates
come from `33bab678`; neither contains #382. Its database migration also requires
upgrade/restore validation. The sync itself does not deliver or delete tokens on
ACM spokes.

For #966, use its [pinned source documentation](https://github.com/opendatahub-io/odh-model-controller/blob/3d892d76afeb92d59d0bdec7baf0dc0892081f79/docs/grid-inference-providers.md),
not the older lab runbook or PR description: namespace label
`grid.opendatahub.io/allowed` and Gateway annotation
`grid.opendatahub.io/serving` configure publication. There are no required
`GRID_*` environment overrides or per-model opt-in annotations in this source.
Render the matching controller permissions and configure the service account,
token Secret and model access grants. Do not carry the old Deployment env patch.

## Already available in the inspected ODH source

GitHub comparisons confirm the merge commits of Grid #319, #336, #341 and #354
are ancestors of ODH `33bab67860eae24717155a0a61c4bab3be2f36fd`
(`behind_by: 0`, merge base equals each required merge).
All three newer Grid candidate images report that source revision.

| Requirement | Upstream merge |
|---|---|
| #319 measured availability | `0b66db51446eda611f273c3146f6ad988f3ff1ad` |
| #336 local-operator signal relay | `d623736d4f20dd13444fc2982e2a59ca82432fae` |
| #341 derived topology | `8c65f1ea76b7bad4b96ec5b851f5686859d87b9c` |
| #354 generated platform install, private endpoints and projected-credential parser | `55b81c240afb8074b3fca586b544f46e08de7245` |

AIGateway #148 is already the source of its inventoried ODH image at `d85a01a7`.
These requirements need compatible image/manifest selection and acceptance, not
another cherry-pick. Registry labels plus source ancestry do not prove runtime
behavior or signed build provenance.

## Conditional changes, not selected

- [Grid #386](https://github.com/praxis-proxy/grid/pull/386), inspected head
  `82746adee71bd725f733e87a01742edb55f90901`: fresh-pressure and provider-revision
  correctness. If selected, patch both operator and gateway because it changes
  signal publication and consumption. It is not a gateway-only swap.
- [Grid #384](https://github.com/praxis-proxy/grid/pull/384), head
  `597b7dadd84b67532cbce33d107cf0bc3053874a`: explicit Cargo package version for
  Hermeto, workspace version alignment and test cleanup. Consider for a new Grid
  build when its recipe needs it; not a reason to rebuild a usable published image.
- Open Grid #324 overlaps merged #341 but uses a different TLS API. Do not add it
  merely because it is still open. Test-only or documentation PRs are not runtime
  carry patches unless their validation is needed for a selected change.
- Downstream Konflux FIPS-policy work is build-pipeline configuration, not a runtime
  payload patch or evidence of FIPS compliance.

## Integration work still required

1. Refresh chart and AIGateway-vendored Grid CRDs/manifests for SiteInvite, then
   propagate the matching pins into the platform payload. Avoid a second owner
   for platform-managed CRDs.
2. Implement ACM token delivery and pruning separately from persistent namespace,
   CA and site identity resources. Track offline cleanup and pre-existing Secrets.
3. Define and validate the [MaaS-to-Grid trusted proxy contract](auth-flow.md).
   #966 handles only the destination's configured Grid identity and flow-control
   headers; it is not the complete front-door delegation implementation.
4. Resolve or explicitly constrain [Grid #248](https://github.com/praxis-proxy/grid/issues/248)
   before accepting provider endpoints with base paths. Also test final-hop TLS
   listener/SNI matching, provider-side authorization, credential mounts, first
   rollout and model withdrawal with the selected sources.
5. Validate model publishing permissions, provider allowlisting and readiness
   metrics. Do not equate successful registration with usable inference.

No extra MaaS API/controller or KServe image patch has been identified by this
assessment. Prefer their published ODH candidates until a reproduced integration
gap establishes a specific required change.
