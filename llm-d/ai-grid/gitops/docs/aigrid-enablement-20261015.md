# aiGrid enablement on Dagobah — 2026-10-15

The first platform enablement gate passed at 2026-10-15T05:20:20.229705+00:00, **with image
overrides and temporary supplemental RBAC**. This does not qualify the complete
MaaS/Grid payload. The original PR image did not deploy the Grid operator unaided.

## Result

- Four CRDs are Established: agenttoolproviders, gridnetworks, gridsites and
  inferenceproviders in grid.praxis.fast.
- opendatahub/grid-operator is 1/1 available, with current observed generation
  and a controller ownerReference to default-aigateway.
- AIGateway and DSC both report AIGridReady=True; DSC is Ready.
- Grid CRDs have no ownerReferences. Their installation was driven by AIGateway;
  do not infer garbage-collection behavior from the Deployment ownership.
- MaaS, batch and all other DSC components are Removed. Platform infrastructure,
  including its shared data-science Gateway, is still reconciled.
- No Grid gateway/enrollment runtime or spoke was deployed. SiteInvite is not in
  these four CRDs. The brief manual MaaS/model canary was removed before this test.

[Sanitized evidence](../tests/aigrid-enablement/result.json) records imageIDs,
conditions and ownership. [DSC](../tests/aigrid-enablement/dsc.yaml) and
[DSCI](../tests/aigrid-enablement/dsci.yaml) record the applied inputs.

## Tested images and provenance

| Component | Immutable image | Source |
|---|---|---|
| Platform operator, manager and copy-manifests init container | quay.io/opendatahub/opendatahub-operator@sha256:f5a19bc4470b2c55b1fd74bb5def8cf253f42103f6d17b511a21606632adf9fa | PR head 2cef9e4d, CI merge a29fb82a |
| AIGateway, manager and copy-manifests init container | quay.io/opendatahub/odh-ai-gateway-operator@sha256:731b59e70a62b4de2545d47eff127c0117a9243dea90b3b9a913043ec3853885 | opendatahub-io/ai-gateway-operator d85a01a7 (#148) |
| Grid operator | quay.io/opendatahub/odh-praxis-ai-grid-operator@sha256:97e45bf92d6280dc8eea710dda28bff3537b0d82a9523cbd9fe7e9af01166c40 | opendatahub-io/praxis-ai-grid-operator 33bab678 |

The platform image came from [CI run 38679396428](https://github.com/opendatahub-io/opendatahub-operator/actions/runs/38679396428).
Its build log checks out a29fb82a887f5539c70eeed3c2564579df92e2ed, merging PR head
2cef9e4deb91b9929c026415fe1c5a313128a34c into main ba1f5efc.
The discovered tag was v3.6.0-ea.2-pr-4186; only its digest was deployed.
The image's revision labels describe the UBI base, so use CI provenance rather
than treating those labels as the platform source revision. Signatures were not
verified. Direct development installation reports platform version 0.0.0.

## Failure and correction

The original image bundles AIGateway e2635889 and pins image d0e588a2. With
aiGrid Managed it installed the four CRDs and reported AIGridReady=True, but
created no Grid operator Deployment.

Changing only the AIGateway image to d85a01a7 exposed stale RBAC in those embedded
manifests. Kubernetes rejected creation of the metrics token Role and Grid
ClusterRoles because the AIGateway service account did not hold their permissions.

The lab used the two RELATED_IMAGE overrides above plus the exact added RBAC
rules from #148. The scraper token permission was constrained to the opendatahub
namespace and grid-operator-metrics-scraper service account; the other added
rules cover GET /metrics, Grid resources/status and named Grid role/binding
lifecycle. No cluster-admin or RBAC escalation permission was added.

The temporary objects are in
[metrics-token-rbac.yaml](../tests/aigrid-enablement/metrics-token-rbac.yaml).
Its historical ClusterRole name ends in metrics-read-permission, but it now
contains the complete missing #148 rule set.

## Reproduce the manual check

Install the platform from its matching source and digest using the repository's
direct deployment procedure (config/default). Use all ODH related-image values
from that source's manifests-config.yaml, with these two overrides:

- RELATED_IMAGE_ODH_AI_GATEWAY_OPERATOR_IMAGE: the AIGateway digest above.
- RELATED_IMAGE_ODH_PRAXIS_AI_GRID_OPERATOR_IMAGE: the Grid operator digest above.

For the old platform digest in this record, also apply the supplemental RBAC.
Apply the recorded DSCI, then DSC. The enabling input is:

```yaml
spec:
  components:
    aigateway:
      managementState: Managed
      aiGrid:
        managementState: Managed
      modelsAsAService:
        managementState: Removed
      batchGateway:
        managementState: Removed
```

Verify CRD Established conditions, Deployment availability and current
observedGeneration, actual pod imageIDs, AIGateway ownership, and both
AIGridReady conditions. Checking only that the field was accepted or the CRDs
exist would have missed the stale-image failure.

## Permanent payload correction and next gate

[Platform PR #4186](https://github.com/opendatahub-io/opendatahub-operator/pull/4186)
commit 60a22cacf6cfdc6d16d0f1b27af3a1fd973cb7c5 refreshes the ODH AIGateway manifest reference and image together,
plus the Grid image. The rebase onto ba1f5efc was a no-op.

Manifest download, ODH overlay rendering, related-image validation, generation,
formatting, lint and focused module tests passed. The rendered AIGateway RBAC
contains the required #148 rules.

Next, resolve the rebuilt PR image by digest, deploy it, confirm the
platform-managed AIGateway role contains the required rules, remove all four
temporary supplemental RBAC objects, and recheck reconciliation. That unassisted
installation remains untested. Only then proceed to MaaS and one inference path.

The new Argo root still renders zero workload Applications. This was a manual
platform test; its resources are not yet managed by the experiments root.
