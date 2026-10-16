# MaaS / AI Grid image inventory

The old stack was removed on 2026-10-15 UTC; observed baseline images below are
historical references. The complete payload remains unselected. A manual aiGrid
enablement subset now runs the rebuilt PR image without supplemental RBAC.
See the [clean-slate record](clean-slate-20261015.md).

Maintained inventory for this rollout. Update it whenever a chart, image, source
revision or custom patch changes, in the same commit as the payload change.

The registry repository answers **where to pull the image**; the source repository
answers **where to change/build it**. Full candidate image digests and related-image
keys are in [image-inventory.yaml](../image-inventory.yaml). Final selections and
custom patch decisions belong in [payload.yaml](../payload.yaml). Its artifact map
is still empty: none of these candidates is a completed deployment selection.

## Manual aiGrid enablement

The [2026-10-15 test record](aigrid-enablement-20261015.md) pins the PR platform
image and the AIGateway/Grid images used on Dagobah. Grid CRDs and the operator
are ready after the payload source fix in PR #4186 at 60a22cac. The rebuilt
platform digest f6a8ecec passed fresh reconciliation after all temporary RBAC was
removed. This verifies an existing-install update, not a clean-install lifecycle
or full MaaS rollout. Exact test selections are in
labValidation in both YAML inventories; artifacts remains empty for the full stack.

## Candidate inspection baseline

Checked 2026-10-15 UTC: the candidates below were readable from their registry,
and their source URL/revision labels were inspected for the host's amd64 image.
This is not signature verification, a complete architecture check, proof of patch
ancestry, or a runtime compatibility test. Moving tags were resolved to immutable
digests in the inventory; do not deploy the tag itself.

The original candidate inspection used platform PR 4186 at f1d5a3e. Three newer Grid images were
discovered from ODH odh-stable. They all report source revision 33bab678, which
syncs upstream Grid 2f1bf91. The platform base still pins an older Grid operator
from f2cf900e. Compare patch ancestry, manifests/CRDs and runtime configuration
before selecting the newer set; do not mix these accidentally.

## Published ODH candidates

| Component / scope | Container registry repository | Build source repository | Observed source commit |
|---|---|---|---|
| KServe controller — OpenShift serving stack | `quay.io/opendatahub/kserve-controller` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| LLMInferenceService controller — LLM serving | `quay.io/opendatahub/odh-kserve-llmisvc-controller` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| KServe module operator — Platform-managed KServe | `quay.io/opendatahub/odh-kserve-module-operator` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| ODH model controller — Serving integration; retained ODH base predates merged PR 966 | `quay.io/opendatahub/odh-model-controller` | [opendatahub-io/odh-model-controller](https://github.com/opendatahub-io/odh-model-controller) | [f60bdb3f](https://github.com/opendatahub-io/odh-model-controller/commit/f60bdb3fb7be5bedddb8dd7bb5531d8b878fb0aa) |
| Model serving API — KServe module payload | `quay.io/opendatahub/odh-model-serving-api` | [opendatahub-io/odh-model-controller](https://github.com/opendatahub-io/odh-model-controller) | [f60bdb3f](https://github.com/opendatahub-io/odh-model-controller/commit/f60bdb3fb7be5bedddb8dd7bb5531d8b878fb0aa) |
| AI Gateway operator — MaaS/Grid component lifecycle | `quay.io/opendatahub/odh-ai-gateway-operator` | [opendatahub-io/ai-gateway-operator](https://github.com/opendatahub-io/ai-gateway-operator) | [d85a01a7](https://github.com/opendatahub-io/ai-gateway-operator/commit/d85a01a7a62e6860ed8799fca857dca2438bfc08) |
| AI gateway payload processing — MaaS gateway processing when enabled | `quay.io/opendatahub/odh-ai-gateway-payload-processing` | [opendatahub-io/ai-gateway-payload-processing](https://github.com/opendatahub-io/ai-gateway-payload-processing) | [2e5c2b86](https://github.com/opendatahub-io/ai-gateway-payload-processing/commit/2e5c2b86c3d8d350cdf6242bc85c9b145f5ecc29) |
| AI Gateway controller — Conditional on selected Gateway API configuration | `quay.io/opendatahub/odh-ai-gateway-controller` | [opendatahub-io/ai-gateway-controller](https://github.com/opendatahub-io/ai-gateway-controller) | [c767f608](https://github.com/opendatahub-io/ai-gateway-controller/commit/c767f608c480d2a7bf2468d4a0dac20b4832b4ca) |
| Praxis external processor — Conditional on selected gateway processing configuration | `quay.io/opendatahub/odh-praxis-extproc` | [opendatahub-io/praxis-extproc](https://github.com/opendatahub-io/praxis-extproc) | [4bc3e5d2](https://github.com/opendatahub-io/praxis-extproc/commit/4bc3e5d2939e4c6a005ff3f26ddc9c2373ffeae1) |
| Grid operator: platform base pin — Older base candidate; compare with newer coordinated Grid set | `quay.io/opendatahub/odh-praxis-ai-grid-operator` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [f2cf900e](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/f2cf900e9b7bba0868e15826353bc16beff78ec0) |
| MaaS API — MaaS hub | `quay.io/opendatahub/maas-api` | [opendatahub-io/models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | [9ed2ce4a](https://github.com/opendatahub-io/models-as-a-service/commit/9ed2ce4a34733c03262cc8f406abffa281c5cf7f) |
| MaaS controller — MaaS hub and its managed resources | `quay.io/opendatahub/maas-controller` | [opendatahub-io/models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | [9ed2ce4a](https://github.com/opendatahub-io/models-as-a-service/commit/9ed2ce4a34733c03262cc8f406abffa281c5cf7f) |
| MaaS discovery — MaaS discovery resources when enabled | `quay.io/opendatahub/odh-maas-discovery` | [opendatahub-io/models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | [9ed2ce4a](https://github.com/opendatahub-io/models-as-a-service/commit/9ed2ce4a34733c03262cc8f406abffa281c5cf7f) |
| KServe storage initializer — Model storage/download initialization | `quay.io/opendatahub/kserve-storage-initializer` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| llm-d endpoint picker — LLM inference routing | `quay.io/opendatahub/odh-llm-d-router-endpoint-picker` | [opendatahub-io/llm-d-router](https://github.com/opendatahub-io/llm-d-router) | [6d4fc23a](https://github.com/opendatahub-io/llm-d-router/commit/6d4fc23a79327f6c9055a9c226aed7fecd69086e) |
| llm-d disaggregation sidecar — Conditional disaggregated inference | `quay.io/opendatahub/odh-llm-d-router-disagg-sidecar` | [opendatahub-io/llm-d-router](https://github.com/opendatahub-io/llm-d-router) | [6d4fc23a](https://github.com/opendatahub-io/llm-d-router/commit/6d4fc23a79327f6c9055a9c226aed7fecd69086e) |
| Grid gateway: newer ODH candidate — Hub and provider Grid gateways | `quay.io/opendatahub/odh-praxis-grid-gateway` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [33bab678](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/33bab67860eae24717155a0a61c4bab3be2f36fd) |
| Grid enrollment: newer ODH candidate — Hub enrollment and invite/bootstrap jobs | `quay.io/opendatahub/odh-praxis-grid-enrollment` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [33bab678](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/33bab67860eae24717155a0a61c4bab3be2f36fd) |
| Grid operator: newer ODH candidate — Hub and spokes, with one operator owner per cluster | `quay.io/opendatahub/odh-praxis-ai-grid-operator` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [33bab678](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/33bab67860eae24717155a0a61c4bab3be2f36fd) |
| KServe local-model controller — Optional local model cache | `quay.io/opendatahub/odh-kserve-localmodel-controller` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| KServe agent — Serving agent | `quay.io/opendatahub/kserve-agent` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| AutoGluon server — Optional model runtime | `quay.io/opendatahub/odh-kserve-autogluon-server` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [5722bd6c](https://github.com/opendatahub-io/kserve/commit/5722bd6c40715ab7b6de5c9af98fc66b3b9f28d9) |
| KServe local-model node agent — Optional local model cache | `quay.io/opendatahub/odh-kserve-localmodelnode-agent` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |
| KServe router — InferenceGraph routing | `quay.io/opendatahub/kserve-router` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [db12bf00](https://github.com/opendatahub-io/kserve/commit/db12bf00b8d97e23bbed2ba02a75715bf97776f4) |


All entries are candidates. Scope labels distinguish the core stack from images
needed only by selected features. The final render and live pod imageIDs determine
the actual required set, including init containers.

## Packaging and components without a final selection

| Artifact | Registry or current reference | Source | Action still required |
|---|---|---|---|
| Platform operator | quay.io/opendatahub/opendatahub-operator | [opendatahub-operator](https://github.com/opendatahub-io/opendatahub-operator) | Find/verify a build containing PR 4186 and the selected component payload, or publish a custom build |
| Operator bundle / catalog | opendatahub-operator-bundle / opendatahub-operator-catalog under the configured publishing registry | Same platform repository and its image/catalog workflows | Required if using OLM; verify publishing destination and pin digests with matching CSV, manifests and related images |
| Installation chart | Existing lab OCI repository: quay.io/sbatsche/rhai-on-openshift-chart | [odh-gitops PR 181](https://github.com/opendatahub-io/odh-gitops/pull/181) | Select/publish the chart from b226467 or its validated successor; record chart version, package digest and source revision |
| Grid overlay-sync | ghcr.io/praxis-proxy/grid-overlay-sync:v0.2.0 | [praxis-proxy/grid](https://github.com/praxis-proxy/grid) | Optional sidecar, disabled by default; no ODH counterpart verified here; resolve a digest if enabled |
| Enrollment PostgreSQL | quay.io/sclorg/postgresql-16-c9s:20260923@sha256:b508e74304788fb4a5a2af76d8d013f20a18ee38bff3d24a705d7b730b8c0a30 | [postgresql-container](https://github.com/sclorg/postgresql-container) | Current parent-chart pin, not re-inspected in this pass; unnecessary as a chart workload if using an external enrollment DB |
| MaaS PostgreSQL | Observed on Dagobah: registry.redhat.io/rhel9/postgresql-16@sha256:510ac645787b2ba27f6ab8bb8488656d025d8c2ddcbf8cc5b15447dac941687b | Database image family: [postgresql-container](https://github.com/sclorg/postgresql-container); MaaS setup in [models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | Select a supported DB arrangement and immutable image; this is an observed baseline, not a new-payload selection or verified source-commit mapping |
| Model runtime | Observed: registry.redhat.io/rhaii/vllm-cuda-rhel9@sha256:c056e61672b6aea489ad5dde0bd2f8497230f5333e87f7cf6c494eba3bfdc808 | Exact downstream build source/revision not inspected | Select per model, accelerator and serving requirements; do not assume an ODH replacement is interchangeable |
| MaaS bootstrap/helper images | RELATED_IMAGE_UBI_MINIMAL_IMAGE and RELATED_IMAGE_ODH_PYTHON_312_IMAGE are forwarded by the payload | Selected Build-Config/CSV plus MaaS manifests | Resolve exact images from the assembled payload; include if rendered or created by controllers |
| Gateway chart smoke test | docker.io/library/busybox:1.37@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e | [BusyBox](https://busybox.net/); image packaging source not inspected | Test-only default; needs sh/nc; not a serving workload |

Chart PR 181 currently defaults to upstream GHCR Grid gateway/enrollment images,
not these ODH candidates. Use its supported related-image overrides and inspect
the render. A repository/tag change alone can leave a default digest in effect.
The override keys are:

- RELATED_IMAGE_ODH_PRAXIS_AI_GRID_GATEWAY_IMAGE
- RELATED_IMAGE_ODH_PRAXIS_AI_GRID_ENROLLMENT_IMAGE
- RELATED_IMAGE_ODH_PRAXIS_AI_GRID_POSTGRES_IMAGE
- RELATED_IMAGE_ODH_PRAXIS_AI_GRID_OVERLAY_SYNC_IMAGE
- RELATED_IMAGE_ODH_PRAXIS_AI_GRID_TEST_IMAGE

The Grid operator is supplied through the platform's
RELATED_IMAGE_ODH_PRAXIS_AI_GRID_OPERATOR_IMAGE, not a second operator subchart.
Overlay-sync is a dedicated binary; do not replace its image with a generic CLI.

## Custom payload overlay

| Change | Affected artifacts | Current disposition |
|---|---|---|
| Platform PR 4186 | Operator, embedded manifests, bundle and catalog | Lab operator at PR head 17b556f2 / build 710e8cc7; full bundle/catalog qualification pending |
| odh-gitops PR 181 | Installation chart and runtime manifests | Carry at b226467; refresh for SiteInvite still required |
| Model-controller PR 966 | Controller image, RBAC and Gateway/namespace configuration | Merged controller image verified at 9b1822e4; custom module RBAC overlay deployed; protected serving acceptance pending |
| Grid SiteInvite PR 382 / ODH sync PR 35 | Enrollment image, SiteInvite CRD and chart RBAC/environment | Required; prefer published ODH build after sync, otherwise custom carry |
| Grid PR 386 | Operator and gateway together | Candidate, not selected |
| Grid PR 384 | Grid build inputs | Conditional build fix, not selected |
| Grid PRs 319, 336, 341, 354; AIGateway PR 148 | Corresponding images | Included in inspected ODH sources; no separate carry |

[PR #966](https://github.com/opendatahub-io/odh-model-controller/pull/966) merged into `incubating` on 2026-10-15T17:03:58Z, at `9b1822e44b98f85fb5c075cae6ee0f33ab6a3f80`.
The merged controller image is `quay.io/opendatahub/odh-model-controller@sha256:4140583d6b4ddab9a490db2a14f833e3f0806f8e9d2837eb0dcdd81e862e89d2`.
Its source labels match the merge commit, its amd64 manifest is anonymously accessible,
and its [Konflux build](https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/open-data-hub-tenant/pipelinerun/odh-model-controller-on-push-fcpcr) succeeded.
This image replaces the PR-head candidate in the applied rollout. The previous
PR-head image and original ODH base remain recorded with their source provenance.
The merged controller is deployed and Ready. Signature verification, provider publication
and protected serving acceptance remain open.
See [the controller image evidence](../tests/serving-payload/merged-controller-verification.json).

The subsequently inspected public KServe module, `quay.io/opendatahub/odh-kserve-module-operator@sha256:5d4b544740ab8af11a6d8b4308cf04223ec7b8d5bcb989fc6ea1b1c8f8ec6a84`,
still lacks the merged Grid RBAC. Its 573 manager and embedded manifest files match
the retained module base. The custom module overlay's role is byte-identical to the
merged #966 role, so the overlay remains required. See
[the module comparison](../tests/serving-payload/module-after-966-verification.json).

KServe is required for this payload. Its controller/module/agent images now match
the platform source `60a22cac` pins at KServe `db12bf00`; EPP and the disaggregation
sidecar match llm-d-router `6d4fc23a`. Earlier candidates remain in
`supersededCandidates` with their provenance. Model runtime validation remains open.

The downloaded KServe module image lacks Grid InferenceProvider RBAC. A local
image overlays only #966's generated `config/rbac/role.yaml`; the module already
has permission to reconcile that named ClusterRole. Both KServe and model-controller
Kustomize renders pass, with the #966 controller image and Grid permissions present.
The build recipe and evidence are in `tests/serving-payload/`. This image is
deployed and the KServe controllers are Ready. `customBuilds` records its local image ID separately
from its registry digest, plus the Praxis and controller-configuration work. Each
entry tracks source, base image, build status, validation and deployment state.

All three custom images are now built locally. The Praxis release image passed
the unchanged-Envoy JSON/SSE test for normal and chunked uploads; first SSE events
arrived before completion. Its workspace tests, standard Clippy and format checks
passed with the release feature set. The extra pedantic pass reports existing
warnings, recorded in the YAML inventory. AI Gateway controller unit tests passed,
and its source patch changes only the dedicated post-processor config and its test.

All three custom builds were pushed through `quay.io:443/sbatsche`.
Deployment pulls use `quay.io/sbatsche` without an explicit port. The inventory
records push destinations separately from immutable pull references; registry
config digests match the local builds.
Publishing required explicitly selecting the existing owner credential. The robot
has push/pull access to the KServe module repository; Praxis and controller
permissions remain absent as of the publication check.
After repository access was fixed, the recheck at 2026-10-15T20:54:00.361712+00:00 passed
for all three images anonymously and with Dagobah's existing pull credentials.
The custom images are deployed. KServe, LLMInferenceService and the merged #966
model controller are Ready. Live remote Grid JSON/SSE passed with normal and
chunked uploads; the Envoy image and all eight filter specifications are unchanged.
See [the rollout record](serving-rollout-20261015.md) for image recovery, controller
memory tuning, observed digests, test evidence and the remaining protected EPP test.

| Custom build | Immutable pull reference |
|---|---|
| kserve-module-pr966 | `quay.io/sbatsche/odh-kserve-module-operator@sha256:b6823d5244387a5f3cbfe3e5606dbf16a43fc23b33fd3f9fafffa08920821771` |
| praxis-streaming | `quay.io/sbatsche/odh-praxis-extproc@sha256:1e7eb83e1b01d6bcb423443d309a8368371c85e3b0021bc77b88ec2793f18be9` |
| ai-gateway-controller-praxis-config | `quay.io/sbatsche/odh-ai-gateway-controller@sha256:57c81815e00c9b986a6b6d2083b019b54e216d0b8813f4e7ef65982cdc806f7f` |

All three images are deployed in the lab; this is not full payload qualification.
Local image IDs and exported manifest digests
remain separate from registry digests. Source patches, SHA-256 hashes, recipes and
test evidence are stored alongside the inventory in the local worktree.

See [the carry assessment](carry-patches.md) for exact revisions, ancestry evidence
and integration work. All selected images use digests, including versioned and
custom builds; tag-only optional entries remain unresolved until pinned.

For each custom replacement, record the base image/revision, ordered patch commits,
resulting source revision, build recipe and build URL, published immutable digest,
validation result and retirement condition. Keep the ODH base candidate visible.
Update payload.yaml with the final selected artifact and cross-reference the patch;
a custom image must not silently replace the inventory's source/provenance record.

The retired lab stored an older custom model-controller image under the
quay.io/sbatsche/grid-operator registry repository. Its repository name is not its
component identity. Do not reuse that reference as the next model-controller
selection without checking the source and patch version.

## Shared infrastructure and optional scope

Reuse the existing ACM, OpenShift GitOps, cert-manager, RHCL/Kuadrant, Service Mesh,
LWS, GPU/networking and storage operators where compatible. Their operator catalogs
and operand image sets remain under their existing owners. Record versions and
resolved image sets separately before a complete disconnected/mirror manifest is
claimed; this document is not yet that complete mirror list.

Istio gateway proxies, Authorino and Limitador remain necessary when the chosen
MaaS configuration uses them. Keep those vendor-managed image pins consistent with
their operators rather than replacing them with unrelated ODH images.

Dashboard/portal, batch gateway, local-model caching and other optional capabilities
are not included as selected workloads. If enabled, expand this inventory from
their rendered and controller-created resources. The portal PoC has no deployment
image selected for this rollout.

## How we keep this current

1. Update the candidate/source map and payload patch/artifact selection together.
   Values, operator CSV/related images, embedded manifests and chart versions must
   agree. Do not automatically promote odh-stable or a PR's latest head.
2. Inspect each new immutable image's manifest and source labels; verify required
   patch ancestry, architecture and available build provenance. Mark precisely what
   was checked. Keep unresolved source mappings explicitly unresolved.
3. Validate the render and inspect controller-created pods, including init/helper
   containers. Compare imageIDs against selected digests; distinguish per-platform
   child manifests from a multi-architecture index when applicable.
4. Record end-to-end validation before declaring the set deployable. On rollback,
   restore the corresponding inventory and payload revision along with manifests;
   database/identity state follows its separate restore procedure.
5. When a published ODH build incorporates a custom patch, replace it only after
   validating the behavior and recording the replacement/retirement decision.

Read [payload maintenance](payload.md) for the artifact promotion gates and
[the rollout](rollout.md) for deployment ordering.

## Manual MaaS/Grid canary selections (2026-10-15)

The [integration record](maas-grid-integration-20261015.md) now identifies a
working manual inference path. `manualCanarySelections` in the YAML inventory
records the observed deployment pins separately from the earlier candidate list.
The AI Gateway controller selection is now source `5502164817b40c8e1760ed50758cfc232fc1f2d4`,
image digest `cd8414da8ab3456ee7cab9ea9702559533757d35139023d372a5e8252d9a0ec9`.
The older `c7a636a4` candidate failed the tenant-local extproc lookup.

The canary uses Grid source `33bab678`, MaaS `9ed2ce4a`, Praxis extproc `4bc3e5d2`,
and the existing chart PR #181 pin. PostgreSQL, vLLM, retained gateway/Kuadrant
images, and the digest-pinned Katan test image are included in the new section.
Unknown source revisions remain explicitly unverified. Image signatures were
not checked. The helper CronJob still uses `registry.redhat.io/ubi9/ubi-minimal:9.7`;
this is a recorded exception and blocks claiming a complete deployable image lock.
The observed gateway reports Envoy 1.34.2-dev and needs the compatibility
qualification described in the integration record.

## Local Qwen workload selection

The GitOps local-model chart pins two Qwen3-Coder-30B-A3B instances and their
weights revision in `values/lab.yaml`. Runtime provenance is explicit: ODH KServe's
GPU template selects Red Hat AI Inference Server 3.2.4 (`fc68d623…`), while EPP
(`872de481…`) and storage initializer (`109a321a…`) are ODH builds. Registry access
and OCI labels were checked using existing cluster credentials; this is not
signature verification or runtime qualification. See
[image evidence](../tests/local-serving/serving-images.json) and
[rollout details](local-qwen-serving-20261015.md).


Local Qwen publication is verified; runtime inference remains pending. The EPP
index digest `872de481` selects amd64 child `7f8a0ec7`, matching both live pods;
see [index evidence](../tests/local-serving/epp-image-index.json). This verifies
manifest selection, not image signatures. See the
[writer handoff](docs-writer-handoff-20261016.md) for qualification boundaries.
