# MaaS / AI Grid image inventory

Maintained inventory for this rollout. Update it whenever a chart, image, source
revision or custom patch changes, in the same commit as the payload change.

The registry repository answers **where to pull the image**; the source repository
answers **where to change/build it**. Full candidate image digests and related-image
keys are in [image-inventory.yaml](../image-inventory.yaml). Final selections and
custom patch decisions belong in [payload.yaml](../payload.yaml). Its artifact map
is still empty: none of these candidates is a completed deployment selection.

## Current verification

Checked 2026-10-15 UTC: all 19 candidates below were readable from their registry,
and their source URL/revision labels were inspected for the host's amd64 image.
This is not signature verification, a complete architecture check, proof of patch
ancestry, or a runtime compatibility test. Moving tags were resolved to immutable
digests in the inventory; do not deploy the tag itself.

The base comes from platform PR 4186 at f1d5a3e. Three newer Grid images were
discovered from ODH odh-stable. They all report source revision 33bab678, which
syncs upstream Grid 2f1bf91. The platform base still pins an older Grid operator
from f2cf900e. Compare patch ancestry, manifests/CRDs and runtime configuration
before selecting the newer set; do not mix these accidentally.

## Published ODH candidates

| Component / scope | Container registry repository | Build source repository | Observed source commit |
|---|---|---|---|
| KServe controller — OpenShift serving stack | `quay.io/opendatahub/kserve-controller` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [c2206314](https://github.com/opendatahub-io/kserve/commit/c220631408b5c2653c9f132940f478d5ca097a87) |
| LLMInferenceService controller — LLM serving | `quay.io/opendatahub/odh-kserve-llmisvc-controller` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [c2206314](https://github.com/opendatahub-io/kserve/commit/c220631408b5c2653c9f132940f478d5ca097a87) |
| KServe module operator — Platform-managed KServe | `quay.io/opendatahub/odh-kserve-module-operator` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [c2206314](https://github.com/opendatahub-io/kserve/commit/c220631408b5c2653c9f132940f478d5ca097a87) |
| ODH model controller — Serving integration; custom publisher patch decision pending | `quay.io/opendatahub/odh-model-controller` | [opendatahub-io/odh-model-controller](https://github.com/opendatahub-io/odh-model-controller) | [f60bdb3f](https://github.com/opendatahub-io/odh-model-controller/commit/f60bdb3fb7be5bedddb8dd7bb5531d8b878fb0aa) |
| Model serving API — KServe module payload | `quay.io/opendatahub/odh-model-serving-api` | [opendatahub-io/odh-model-controller](https://github.com/opendatahub-io/odh-model-controller) | [f60bdb3f](https://github.com/opendatahub-io/odh-model-controller/commit/f60bdb3fb7be5bedddb8dd7bb5531d8b878fb0aa) |
| AI Gateway operator — MaaS/Grid component lifecycle | `quay.io/opendatahub/odh-ai-gateway-operator` | [opendatahub-io/ai-gateway-operator](https://github.com/opendatahub-io/ai-gateway-operator) | [d85a01a7](https://github.com/opendatahub-io/ai-gateway-operator/commit/d85a01a7a62e6860ed8799fca857dca2438bfc08) |
| AI gateway payload processing — MaaS gateway processing when enabled | `quay.io/opendatahub/odh-ai-gateway-payload-processing` | [opendatahub-io/ai-gateway-payload-processing](https://github.com/opendatahub-io/ai-gateway-payload-processing) | [2e5c2b86](https://github.com/opendatahub-io/ai-gateway-payload-processing/commit/2e5c2b86c3d8d350cdf6242bc85c9b145f5ecc29) |
| AI Gateway controller — Conditional on selected Gateway API configuration | `quay.io/opendatahub/odh-ai-gateway-controller` | [opendatahub-io/ai-gateway-controller](https://github.com/opendatahub-io/ai-gateway-controller) | [c767f608](https://github.com/opendatahub-io/ai-gateway-controller/commit/c767f608c480d2a7bf2468d4a0dac20b4832b4ca) |
| Praxis external processor — Conditional on selected gateway processing configuration | `quay.io/opendatahub/odh-praxis-extproc` | [opendatahub-io/praxis-extproc](https://github.com/opendatahub-io/praxis-extproc) | [4bc3e5d2](https://github.com/opendatahub-io/praxis-extproc/commit/4bc3e5d2939e4c6a005ff3f26ddc9c2373ffeae1) |
| Grid operator: platform base pin — Older base candidate; compare with newer coordinated Grid set | `quay.io/opendatahub/odh-praxis-ai-grid-operator` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [f2cf900e](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/f2cf900e9b7bba0868e15826353bc16beff78ec0) |
| MaaS API — MaaS hub | `quay.io/opendatahub/maas-api` | [opendatahub-io/models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | [9ed2ce4a](https://github.com/opendatahub-io/models-as-a-service/commit/9ed2ce4a34733c03262cc8f406abffa281c5cf7f) |
| MaaS controller — MaaS hub and its managed resources | `quay.io/opendatahub/maas-controller` | [opendatahub-io/models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | [9ed2ce4a](https://github.com/opendatahub-io/models-as-a-service/commit/9ed2ce4a34733c03262cc8f406abffa281c5cf7f) |
| MaaS discovery — MaaS discovery resources when enabled | `quay.io/opendatahub/odh-maas-discovery` | [opendatahub-io/models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) | [9ed2ce4a](https://github.com/opendatahub-io/models-as-a-service/commit/9ed2ce4a34733c03262cc8f406abffa281c5cf7f) |
| KServe storage initializer — Model storage/download initialization | `quay.io/opendatahub/kserve-storage-initializer` | [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve) | [8f0e500f](https://github.com/opendatahub-io/kserve/commit/8f0e500f7bbff2104dabfdd95f22e80476465634) |
| llm-d endpoint picker — LLM inference routing | `quay.io/opendatahub/odh-llm-d-router-endpoint-picker` | [opendatahub-io/llm-d-router](https://github.com/opendatahub-io/llm-d-router) | [c0adac19](https://github.com/opendatahub-io/llm-d-router/commit/c0adac19e0d16adf3e36a049929ff053ccb1fa72) |
| llm-d disaggregation sidecar — Conditional disaggregated inference | `quay.io/opendatahub/odh-llm-d-router-disagg-sidecar` | [opendatahub-io/llm-d-router](https://github.com/opendatahub-io/llm-d-router) | [c0adac19](https://github.com/opendatahub-io/llm-d-router/commit/c0adac19e0d16adf3e36a049929ff053ccb1fa72) |
| Grid gateway: newer ODH candidate — Hub and provider Grid gateways | `quay.io/opendatahub/odh-praxis-grid-gateway` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [33bab678](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/33bab67860eae24717155a0a61c4bab3be2f36fd) |
| Grid enrollment: newer ODH candidate — Hub enrollment and invite/bootstrap jobs | `quay.io/opendatahub/odh-praxis-grid-enrollment` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [33bab678](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/33bab67860eae24717155a0a61c4bab3be2f36fd) |
| Grid operator: newer ODH candidate — Hub and spokes, with one operator owner per cluster | `quay.io/opendatahub/odh-praxis-ai-grid-operator` | [opendatahub-io/praxis-ai-grid-operator](https://github.com/opendatahub-io/praxis-ai-grid-operator) | [33bab678](https://github.com/opendatahub-io/praxis-ai-grid-operator/commit/33bab67860eae24717155a0a61c4bab3be2f36fd) |


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
| Platform PR 4186 | Operator, bundled manifests/RBAC/CRDs, bundle and catalog when using OLM | Carry; matching custom/published build selection pending |
| odh-gitops PR 181 | Installation chart and runtime manifests | Carry; package selection pending |
| Model-controller PR 966 | ODH model controller and its publisher configuration/RBAC | Candidate custom patch, awaiting confirmation of full patch set; stock image above is only a base |
| Grid-specific open PRs | Grid operator and/or gateway according to the patch | Full carry list not yet confirmed; preserve required lab behavior |
| Previously carried Grid PRs 319, 336, 341; AIGateway PR 148 | Corresponding Grid/AIGateway images | Merged; verify inclusion in the chosen image source before retiring custom builds |

For each custom replacement, record the base image/revision, ordered patch commits,
resulting source revision, build recipe and build URL, published immutable digest,
validation result and retirement condition. Keep the ODH base candidate visible.
Update payload.yaml with the final selected artifact and cross-reference the patch;
a custom image must not silently replace the inventory's source/provenance record.

The existing lab even stores an older custom model-controller image under the
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
