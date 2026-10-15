# Roll out the new stack on Dagobah

The old RHOAI/MaaS/Grid stack was removed with explicit owner approval on
2026-10-15 UTC. Start from the [clean-slate record](clean-slate-20261015.md).
This is a fresh experimental installation using ODH artifacts and explicit custom
patches; it is not an in-place RHOAI upgrade. The empty GitOps root is usable, but
the workload payload remains incomplete.

See [ACM spokes and portal provisioning](acm-gitops-flow.md) for the later
UI/API-to-Helm-values workflow and [the auth flow](auth-flow.md) for trust boundaries.

## 1. Verify prerequisites

Use explicit kubeconfig contexts. Confirm the old stack has not returned, ACM
registrations and nodes are healthy, and the new root still has zero workloads.
Keep shared logging, observability, ingress, storage and networking under their
existing owners. Resolve Dagobah's pre-existing OAuth certificate trust problem
before authentication acceptance.

The former site-a/site-b tenants ran on Dagobah; site-d is the imported k3s cluster.
Site-e remains imported but is not selected for the new three-site layout.
Create fresh databases and enrollment identities. Old database contents and Grid
CA/site identities are not migration inputs.

## 2. Select the payload

Resolve the decisions and blockers in `../payload.yaml`, following
[payload verification](payload.md) and [the carry assessment](carry-patches.md).
Pin selected images by digest, including custom operator, manifests, bundle/catalog,
gateway, enrollment, database and helper images. Record chart/source revisions
and build/provenance evidence alongside each custom replacement.

The model-controller carry needs its RBAC and namespace/Gateway configuration
alongside its image. The pinned PR 966 uses the namespace label
`grid.opendatahub.io/allowed=true` and the Gateway annotation
`grid.opendatahub.io/serving`; see the exact JSON contract in the auth/carry docs.
Do not reproduce the old lab's manual `GRID_*` environment patches.

Inspect rendered MaaS API/controller/database resources, initial schema,
default tenant, Gateway, TLS and authorization/rate-limit policies. Use separate
databases for MaaS and Grid enrollment. SiteInvite requires a matching enrollment
image, CRD, chart/RBAC and token delivery/pruning implementation.

## 3. Establish GitOps ownership

Use the existing `dagobah` root with `values/lab.yaml`. Add one child at a time
and keep sync manual during qualification. The bootstrap project permits only
Applications; create scoped workload AppProjects before adding workloads.

Argo owns installation/runtime configuration. ACM supplies cluster placement,
connectivity and site policy/secret distribution. The platform operator owns
AIGateway; AIGateway owns its Grid operator/CRDs and MaaS components according to
the selected manifests. Assign each object one desired-state owner. Do not deploy
the former standalone Grid operator/chart or reapply the retired `ai-grid` root.

The old `argo-acm` tree still supplies the retained logging and observability
Applications. It is historical source for the removed stack, not its rollout root.

## 4. Install in explicit stages

1. **Prerequisites:** verify compatible installed versions of cert-manager,
   RHCL/Kuadrant, Service Mesh, LWS/jobset, ACM and GitOps. Reuse their existing
   subscriptions/OperatorGroups.
2. **Platform payload:** install one selected platform operator with matching
   manifests, RBAC, CRDs and related images. The old RHOAI subscription is absent.
   Document installation and recovery commands for the chosen bundle/catalog or
   development deployment; do not run overlapping platform operators.
3. **APIs and components:** wait for CRDs and controllers, then create DSC/DSCI
   through the selected owner. Enable KServe, AIGateway, modelsAsAService and aiGrid
   as mandatory components of this payload. The `ai-grid` profile alone does
   not enable all model serving. KServe, LLMInferenceService and model-controller
   readiness, matching RBAC, and a working InferencePool/EPP path are acceptance
   requirements before the payload can be called complete.
4. **MaaS:** initialize a fresh database and verify API/controller readiness,
   tenant, Gateway and policies. Test authentication, authorization and metering
   independently before connecting Grid.
5. **Grid hub:** initialize enrollment and a new Grid CA/network identity. Deploy
   the gateway with actual routing configuration. Restrict the internal listener
   to authenticated MaaS callers; validate TLS and NetworkPolicy enforcement.
6. **Providers:** recreate site-a/site-b tenant models and enroll site-d using fresh
   invitations. ACM delivers the token and CA bundle to the joining operator's
   namespace and removes the delivered token when the invite is pruned. Validate
   namespace/Gateway publishing, model-scoped grants and provider credentials.
   The exact delivery implementation remains a payload qualification task.
7. **Traffic:** validate one canary model/provider, then expand. Record the Git
   commit and artifact digests that pass. Site-e is optional future scope.

Argo sync waves do not replace CRD establishment, database initialization, operator
readiness or end-to-end checks. Separate API installation from resources that use it.

## 5. Acceptance

- Every container/initContainer matches the selected digest-pinned payload.
- KServe is Managed and its module/controller, LLMInferenceService controller and
  #966 model-controller reconcile real models into protected serving gateways.
- Each Grid-managed MaaS model references only ExternalProvider/grid; Grid owns
  its serving candidates. Publication and withdrawal reconcile on both sides.
- JSON and incremental SSE pass local and remote InferencePool/EPP paths using
  the deployed Envoy image and configuration, without an Envoy override.
- DSC/AIGateway report current-generation readiness; one Grid operator reconciles.
- MaaS rejects invalid keys and unauthorized models, admits valid requests and
  enforces token limits, including a tested 429.
- MaaS requests reach intended providers. Direct access by unrelated pods fails;
  spoofed identity/subscription/flow-control headers cannot bypass authorization.
- Provider endpoints, model names, credential mounts and TLS/SNI match the serving
  APIs. The authenticated Grid service identity has the intended model grants.
- Opt-out/removal withdraws provider routes/grants; failed sites stop receiving
  requests within the expected detection interval.
- Controller restart and Argo resync preserve images, namespace/Gateway settings
  and RBAC.
- Invite expiry/pruning, token-copy removal, identity renewal and telemetry work.
- Usage attribution across routing, retries and streaming matches the agreed
  MaaS-to-Grid contract.

Record evidence per check. Ready Deployments and an empty Synced root do not prove
the inference/authentication path works.

## 6. Recovery

Before each stage, save its desired-state commit and protected state backups.
Once databases and enrollment are initialized, test database restore and protect
CA keys, credentials and PVCs. Roll back matching images and manifests together;
never run two platform or Grid reconcilers concurrently. A Git revert does not undo
a schema migration.

There is no preserved old front door/database to switch back to after this reset.
Before the first accepted release, recover by repairing or reinstalling the selected
stage using its documented state plan. After acceptance, restore the last validated
release and compatible database/identity backups. Do not delete CRDs merely to roll
back an image: that deletes their custom resources.
