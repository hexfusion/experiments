# Portal provisioning through Helm, GitOps, and ACM

Design direction for the new MaaS/Grid stack, recorded 2026-10-15.
This is an integration design, not an implemented portal-to-cluster workflow.
The current shared Application chart has no workloads.

## The intended user experience

An administrator selects an existing ACM cluster, assigns it to a site, and clicks
**Add to AI Grid**. The API prepares the Helm values needed to install that
cluster's role. GitOps applies the selected charts and ACM delivers its enrollment
material. The UI reports progress until the spoke is ready.

For these complex provisioning changes, use this flow:

**UI → API prepares values → validate → persist to remote Git → GitOps/ACM → spoke**

This does not require every model or subscription API call to write Git. Those
resources need one authoritative persistence/reconciliation path, to be chosen
when integrating their APIs. GitOps must not independently overwrite resources
owned by a live API/controller.

```mermaid
sequenceDiagram
    actor User
    participant UI as Portal
    participant API as Provisioning API
    participant Git as Remote Git
    participant Argo as Argo CD
    participant ACM as ACM hub
    participant Spoke as Spoke controllers
    User->>UI: Add cluster to site
    UI->>API: Cluster, site, role and options
    API->>API: Authorize, prepare values, validate render
    API->>Git: Persist values and pinned chart/payload references
    Git-->>API: Configuration revision
    API-->>UI: Saved; provisioning pending
    Argo->>Git: Read desired configuration
    Argo->>ACM: Apply placement and delivery configuration
    ACM-->>Argo: Selected and registered destinations
    Argo->>Spoke: Apply Helm-rendered installation resources
    ACM->>Spoke: Deliver invite token and Grid CA bundle
    Spoke->>Spoke: Reconcile installation and enroll
    Note over UI,Spoke: API aggregates observed readiness for the UI
```

The diagram shows responsibilities, not a strict ordering guarantee between
controllers. API establishment, prerequisites and secret delivery must gate the
steps that depend on them. Chart rendering alone does not prove readiness.

## What the example implements

Reviewed [ai-grid-cloud-provider-poc](https://github.com/krisv/ai-grid-cloud-provider-poc/tree/62319b8e3e2d8af34c967dca4f2add51da44d483)
at `62319b8e3e2d8af34c967dca4f2add51da44d483`.

| Area | Implemented behavior | Missing integration |
|---|---|---|
| Add cluster | PATCH adds `ai-grid/enabled: "true"` to a local ManagedCluster file or an existing live ACM cluster | Site/role values, chart deployment and readiness reconciliation |
| Git persistence | Local files, optionally local commits | Remote repository adapter, push and conflict handling |
| Provisioned status | Externally supplied `ai-grid/provisioned` label | Controller deriving readiness from deployment/enrollment results |
| Model creation | Saves LLMInferenceService, MaaSModelRef, Placement and ManifestWorkReplicaSet | Apply/distribution path and live status |
| Request serialization | In-process queue and local filesystem lock | Durable asynchronous operations if requests are accepted before persistence |

Sources: [cluster handler](https://github.com/krisv/ai-grid-cloud-provider-poc/blob/62319b8e3e2d8af34c967dca4f2add51da44d483/backend/clusters.mjs),
[local store](https://github.com/krisv/ai-grid-cloud-provider-poc/blob/62319b8e3e2d8af34c967dca4f2add51da44d483/backend/GitResourceStore.mjs),
[model flow](https://github.com/krisv/ai-grid-cloud-provider-poc/blob/62319b8e3e2d8af34c967dca4f2add51da44d483/docs/model-deployment-ui.md),
[status mapping](https://github.com/krisv/ai-grid-cloud-provider-poc/blob/62319b8e3e2d8af34c967dca4f2add51da44d483/shared/grid-clusters.mjs).

The local store expects its own repository format. Pointing it at experiments is
not sufficient. We need an adapter that produces our values and persists them to
the configured remote repository. Its live ACM label PATCH must also be coordinated
with the chosen owner of membership intent.

## Ownership

| State or action | Owner in the proposed flow |
|---|---|
| Shared charts, payload/image pins and installation defaults | GitOps configuration |
| User authorization, request validation and values preparation | Portal API |
| Complex provisioning intent after successful save | Remote Git configuration |
| Imported cluster inventory, availability and placement | ACM |
| Applying chart-rendered installation resources | Argo CD |
| Reconciling installed components | Platform, AIGateway, Grid and serving operators |
| Delivering invite token and trust material | ACM policies |
| Derived provisioning status and UI progress | Status integration/controller, still to build |

Keep environment settings in `values/lab.yaml` and reuse shared templates.
Argo's ApplicationSet may create per-spoke Applications; we do not need a manually
maintained directory of manifests for every cluster.

On platform-managed OpenShift, DSC/AIGateway owns the Grid operator and CRDs.
Runtime charts install gateway/enrollment workloads. Avoid a second operator owner.
A k3s provider needs a validated standalone operator/runtime path; do not install
the full RHOAI chart there. Provider-only roles should not automatically receive
the hub's MaaS control plane or enrollment database.

## Add an ACM spoke

1. **Import the cluster.** Kubernetes already exists. Confirm the ACM registration
   is accepted, joined and reachable. Cloud infrastructure and Kubernetes creation
   are a separate preceding workflow.
2. **Select site and role.** Keep the ACM cluster name distinct from a geographic
   site grouping. Initially use a unique Grid enrollment identity per cluster.
   Multiple clusters in one logical site must not share invitation tokens or
   identity private keys; their GridSite mapping needs an explicit contract.
3. **Prepare values.** The API selects the chart/payload and prepares validated
   role-specific inputs. Define the spoke-values schema and templates before the
   portal writes new fields into `values/lab.yaml`. Store references to Secrets,
   never invite tokens, private keys or kubeconfigs.
4. **Persist the change.** Validate schema, render, prerequisites and ownership.
   Commit/push to the configured repository and return the saved revision.
   Direct pushes to experiments main are allowed. A retry must identify its
   previous change instead of creating a duplicate installation.
5. **Select and register destinations.** Use ACM Placement and cluster-set bindings.
   GitOpsCluster can register selected clusters with Argo; an ApplicationSet can
   consume PlacementDecisions to instantiate the shared chart. Configure the
   necessary controller permissions. This follows the
   [OCM/Argo integration pattern](https://open-cluster-management.io/docs/scenarios/integration-with-argocd/).
6. **Install and enroll.** Argo applies the role's chart resources. Enrollment on
   the hub creates the site invite; ACM delivers it with the CA bundle into the
   spoke operator namespace. The operator enrolls and maintains its identity.
7. **Confirm readiness.** Aggregate Argo sync/health, ACM availability, operator
   readiness, Grid identity/connectivity and provider checks. Only then mark
   provisioning complete and offer the cluster as a model deployment destination.

Bootstrap selection must not require `ai-grid/provisioned`: that creates a cycle
where an unprovisioned cluster can never receive its installation. Model placement
may require it after installation. A temporarily unavailable spoke must not be
uninstalled just because it disappears from an availability-based selection.

Start with the existing lab's hub-to-spoke connectivity. Disconnected spokes or
Argo pull delivery need a separately validated configuration.

## Values and generated objects

Prefer storing **values and pinned chart/payload references**, with Argo rendering
the objects. When an integration requires static manifests in Git, generate them
from those inputs and verify reproducibility. Do not hand-edit both representations.

The resource types saved by the model UI are useful candidates, not a final
ownership decision. If we keep its ManifestWorkReplicaSet distribution, apply the
Placement and work resources on the hub; ACM delivers the embedded model workload.
Do not independently apply the same LLMInferenceService with an Argo spoke app or
apply its source file as a second model on the hub. Verify the installed ACM feature
and API support before using
[ManifestWorkReplicaSet](https://open-cluster-management.io/docs/concepts/work-distribution/manifestworkreplicaset/).

For user-created models/subscriptions, decide whether the API persists desired state
directly in Kubernetes or prepares Git configuration. Both can reuse chart/resource
generation logic, but each resource must have one authoritative owner.

## Persistence, concurrency and durable work

A simple synchronous save can wait for validation and successful remote Git
persistence. The response means **configuration saved**, not **model running**.
Concurrent writes need revision checks and bounded conflict retries; never resolve
API concurrency by force-pushing over another user's changes.

If the API accepts work before remote persistence, or preparation becomes a long
workflow, it must first write a **durable queued operation**. Return `202 Accepted`
with an operation ID only after that write succeeds. An in-memory queue is insufficient.

A database implementation should atomically save intent and its queue/outbox entry.
Workers need retries and idempotent steps, with protection against stale updates.
Alternatively, a Kubernetes custom resource can hold durable desired work which a
controller rediscovers after restart; the controller's in-memory queue is not itself
durable. The queue/store technology is open, not a prerequisite for chart development.

Kubernetes's controller pattern and resource-version concurrency are available
building blocks for the API-native option.
[Controllers](https://kubernetes.io/docs/concepts/architecture/controller/),
[API concurrency](https://kubernetes.io/docs/reference/using-api/api-concepts/).

The UI should distinguish pending/saved, deploying, ready and failed, and expose
the relevant revision and error. Continue reconciliation after the first successful
deployment: running a provisioning job once does not maintain state indefinitely.

## Invite cleanup and offboarding

Separate the invite token policy from persistent namespace, CA and SWIM-key policies.
For a token created by ACM, use an enforced ConfigurationPolicy with
`pruneObjectBehavior: DeleteIfCreated`; removing the invite must also remove its
token policy/managed object. Deleting only the hub Secret can cause template
resolution errors rather than remove the delivered copy. Pre-existing/adopted
target Secrets need an explicit cleanup decision.
[Policy cleanup reference](https://github.com/open-cluster-management-io/policy-generator-plugin/blob/main/docs/policygenerator-reference.yaml).

The current lab and reviewed chart policies combine these resources and lack token
pruning. Split their ownership before claiming automatic cleanup. Keep namespace
and persistent trust outside the token's deletion scope. If a spoke is offline,
report remote deletion as pending rather than completed.

Removing an invitation is not revocation of an already enrolled identity. Offboard
in stages: stop new placement/routing, drain/remove owned workloads, revoke unused
invites and remove delivered tokens, then apply the site's identity decommission
policy. Preserve shared infrastructure and persistent state unless the operation
explicitly includes their deletion.

## First integration slice and acceptance

1. Select the payload and validate reusable hub/provider chart inputs.
2. Exercise one imported spoke manually with ACM and GitOps.
3. Implement the portal adapter for validated values and remote persistence.
4. Connect membership to placement, chart deployment and isolated secret delivery.
5. Feed observed status back to the UI and exercise retries, restart and offboarding.

A successful test installs the selected role once, enrolls the spoke and reports
observed readiness. Repeating the request is harmless. A Git failure remains visible;
a restart loses no accepted asynchronous work. Removing an invite deletes its remote
token without deleting the namespace or CA.

No portal adapter, queue, workload Application or cleanup controller is implemented
by this document. Continue with [rollout](rollout.md) and
[downstream installation](downstream-installation.md).
