# Roll out the new stack on Dagobah

This runbook is under construction. The bootstrap is usable; workload installation
is gated on the complete payload and a reviewed ownership transition. The baseline
is RHOAI 3.5.1. The target uses ODH artifacts and explicit custom patches to exercise
the new MaaS and AI Grid arrangement.

## 1. Record and protect the baseline

Use an explicit kubeconfig context. Save Subscription/CSV, DSC/DSCI, Argo Application,
ACM policy, Grid and MaaS configuration outside Git. Save Helm values and release
history. Keep Secrets, API keys, invitations, database credentials and private keys
in a protected local backup or secret store; never add them to this repository.
Back up the MaaS and enrollment databases separately and verify their restore path.
Retain enrollment CA/identity material and PVCs: losing them invalidates existing sites.

```bash
kubectl config current-context
kubectl get subscriptions.operators.coreos.com,clusterserviceversions.operators.coreos.com -n redhat-ods-operator
kubectl get datascienceclusters,dscinitializations -o yaml
kubectl get applications.argoproj.io -n openshift-gitops
kubectl get managedclusters
kubectl get deployments -n grid
kubectl get deployments,persistentvolumeclaims -n grid-enrollment
```

Run and record a baseline inference request, token rejection, model authorization
and rate-limit check through the existing MaaS endpoint. Record provider health and
site-d reachability. An existing Ready condition alone is not an end-to-end baseline.

## 2. Select the payload

Resolve the decisions and blockers in `../dagobah/payload.yaml`, following
[payload verification](payload.md). Include the model-controller configuration and
RBAC alongside its image when carrying its Grid publishing patch. The old lab's
manual GRID environment patches disappear when its platform operator recreates the
Deployment; the new configuration must survive reconciliation and restart.

Inspect the rendered MaaS resources from the exact chosen payload. Confirm the
API/controller/database configuration and ownership, migrations, default tenant,
Gateway, TLS, AuthPolicies and rate limiting. New image tags alone do not reproduce
a new MaaS setup. Do not reuse the MaaS database for Grid enrollment.

## 3. Establish GitOps ownership

Bootstrap the new `dagobah` root without modifying the old `ai-grid` root. Add one
child at a time. Argo owns desired installation/runtime configuration; ACM handles
cluster placement, connectivity and site-specific policy/secret distribution. The
platform operator owns AIGateway; AIGateway owns its Grid operator/CRDs and MaaS
components according to the selected manifests. Do not manage those same objects
from a second chart or ACM policy.

Before transferring any existing Application, pause its automated sync and pause
any parent/ACM source that would restore it. Record its resource inventory, deletion
finalizer and prune configuration. Protect namespaces, PVCs, CA material and shared
CRDs from prune/delete. Remove old ownership without cascading resource deletion;
then adopt the same resources under the new desired state. Inspect Argo's diff for
unintended deletes, name changes and duplicate managers before syncing.

Do not run both the old standalone Grid operator and the new AIGateway-managed
operator against the same Grid resources. Establish the new manifests and preserved
state, stop the old reconciler during the handoff, then verify exactly one owner.
Disabling a Helm subchart or deleting an Application is not an ownership transfer.

## 4. Install in explicit stages

1. **Prerequisites:** reuse the installed cert-manager, RHCL/Kuadrant, Service Mesh,
   LWS, ACM and GitOps installations where compatible. Keep one owner for each OLM
   subscription/OperatorGroup, especially ACM-managed observability.
2. **Platform payload:** choose an installation path compatible with the existing
   RHOAI subscription. Do not install an overlapping ODH operator beside it or point
   the RHOAI package at an unrelated ODH catalog. Prepare the matching manifests,
   RBAC, CRDs and related images before the operator handoff. Record the rollback
   procedure for this chosen path before changing the running controller.
3. **APIs and components:** wait for required CRDs and controller readiness, then
   configure the existing DSC/DSCI through its single desired-state owner. Keep
   KServe Managed; enable AIGateway, modelsAsAService and aiGrid as required. The
   `ai-grid` profile alone does not enable the entire MaaS/model-serving stack.
4. **MaaS:** verify API, database, controller, tenant, Gateway and policy resources
   from the selected payload. Validate authentication, authorization and metering
   before connecting the new Grid path.
5. **Grid hub:** preserve enrollment records/CA and network identity. Deploy the
   gateway against an actual routing configuration; the chart's status endpoint is
   not proof of model routing. Restrict the internal gateway to the intended MaaS
   callers using explicit NetworkPolicy peers and verified TLS configuration.
6. **Providers:** retain site-a/site-b model workloads and site-d connectivity.
   Verify opted-in providers, model-scoped grants, generated topology, credential
   mounts and site trust. Move one model at a time. Keep site-e and simulators until
   their replacement is verified and their cleanup is explicitly scheduled.
7. **Traffic:** test the new path with a canary model/endpoint, then switch the
   front door. Record the exact commit and artifact digests that passed.

Argo sync waves order resources, but do not replace CRD establishment, database
migration, operator readiness or end-to-end acceptance gates. Keep APIs and their
consumers in separate sync stages where the old schema prevents diffing new fields.

## 5. Acceptance

- Every deployed container/initContainer matches the selected payload.
- DSC/AIGateway report current-generation readiness; one Grid operator reconciles.
- MaaS rejects invalid keys and unauthorized models, admits valid requests, and
  enforces configured token limits (including a tested 429).
- Requests through MaaS reach each intended provider; the internal Grid listener
  cannot be reached by unrelated pods. Verify policy enforcement, not just YAML.
- Published provider endpoints and model names match the serving APIs. Credentials
  mount correctly and trusted service identity passes provider-side authorization.
- A provider's removal or opt-out removes its route/grants; an unavailable site is
  not selected once the expected failure detection interval has elapsed.
- Reconcile/restart controllers and resync Argo; images, GRID settings and RBAC persist.
- Enrollment/identity renewal and provider telemetry remain healthy.

Record pass/fail and evidence per test. Do not call the full rollout complete based
only on the empty root syncing or Deployments being Available.

## 6. Rollback

Stop the migration/new syncs and return traffic to the last validated front door.
Restore the prior desired-state commit and controller ownership in reverse order;
never start two platform or Grid reconcilers concurrently. Restore image pins and
matching manifests together. Restore database snapshots only according to the
selected version's migration/restore procedure; a Git revert cannot undo a schema
migration. Preserve CA keys, invitations, enrollment records, Secrets and PVCs.
Do not delete CRDs to roll back an image: doing so deletes their custom resources.
Repeat baseline inference and access-control tests before resuming automatic sync.
