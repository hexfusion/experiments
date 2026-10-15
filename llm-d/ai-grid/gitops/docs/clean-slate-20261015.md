# Clean slate: 2026-10-15 UTC

The owner explicitly authorized deletion of all existing RHOAI, MaaS and Grid
resources, including databases, persistent storage and identities. This reset
replaces the earlier plan to preserve and migrate the old stack. The new payload
has **not** been installed.

## Topology and scope

| Cluster | Previous stack | Result |
|---|---|---|
| Dagobah / ACM `local-cluster` | RHOAI 3.5.1; MaaS tenants site-a/site-b; Grid HQ, enrollment and demo tooling | Removed |
| ACM `site-d` | Imported k3s cluster; Grid factory; model providers in `llm-d` and `vllm` | Stack removed; cluster retained |
| ACM `site-e` | Imported OpenShift cluster; RHOAI 3.5.1, MaaS retail tenant and Grid retail | Stack removed; cluster retained |

Site-a and site-b were tenant namespaces on Dagobah, not separate ACM clusters.
The intended new layout remains site-a/site-b plus site-d. Site-e remains imported
and available, with no decision to include it in the new rollout.

## Removed

- Old `ai-grid` Argo root and its enrollment, HQ, factory, retail, front-door,
  CRD and demo-observability Applications. Automated sync was stopped first.
  Applications were deleted without cascading so shared resources could be retained.
- Old enrollment ACM policies and their replicated configuration policies.
- Model services, MaaS subscriptions/auth policies/model references, tenants,
  Grid providers/sites/networks and enrollment resources.
- RHOAI subscriptions and CSVs on both OpenShift clusters; DSC/DSCI and component
  resources; platform, KServe, model-serving, MaaS and Grid controllers.
- Dedicated stack namespaces: hub `grid`, `grid-enrollment`, `grid-load`,
  `grid-demo-grafana`, `ai-tenant-site-a`, `ai-tenant-site-b`, `ai-tenants`,
  `models-as-a-service` and the `redhat-ods-*`/`redhat-ai-gateway-infra` namespaces.
  Site-e's corresponding namespaces included `ai-tenant-retail`; site-d's were
  `grid`, `llm-d` and `vllm`.
- Stack Gateways, routes, policies, certificates, Secrets, processing workloads,
  RBAC and admission configuration in shared namespaces. The RHOAI-managed
  gateway control planes were removed; the Service Mesh operators remain.
- 41 RHOAI/MaaS/KServe/Grid CRDs on each OpenShift cluster and four Grid CRDs
  on k3s. Shared Gateway API, Kuadrant, ACM and other infrastructure APIs remain.
- Old RHOAI CA-bundle copies and model-serving dashboard ConfigMaps, dedicated
  cluster RBAC/SCCs, OAuth client and `rhods-admins` groups.
- Enrollment database PVC/PV on Dagobah and MaaS PostgreSQL PVC/PV on site-e.
  No old MaaS/enrollment database or Grid identity is a preserved rollout input.

KServe protected its built-in configuration templates from ordinary deletion.
After retiring the controllers and webhooks, the remaining KServe/MaaS finalizers
were cleared on the specific orphaned objects. The removed demo Grafana controller
also left 15 dashboard/datasource finalizers; those were cleared individually.
Namespace finalization was not forced.

KServe garbage collection removed a generic `metrics-reader` ClusterRole that
also carried Service Mesh ownership on site-e. Its original shared rule was
restored without the obsolete KServe owner reference.

## Retained

- All clusters/VMs, nodes, ACM registrations and agents; ACM's Argo cluster bridge.
- New empty `dagobah` root/project, with manual sync.
- Shared `grid-logging` and `grid-observability` Applications and their workloads,
  policies, storage and dashboards. These names describe shared infrastructure,
  so a broad `grid*` deletion would have been incorrect.
- OpenShift ingress/router, shared lab ingress certificate/CA, DNS configuration
  and Submariner's `default-broker` namespace.
- ACM, GitOps, Service Mesh, RHCL/Kuadrant, cert-manager, LWS/jobset, GPU,
  networking and storage operators.
- Unrelated workloads, storage and host files. This was a Kubernetes stack reset,
  not a VM/disk wipe.

## Verification and evidence

- Six Dagobah nodes, one site-d node and one site-e node Ready.
- ACM `local-cluster`, `site-d` and `site-e` joined and Available.
- `dagobah`, `grid-logging` and `grid-observability` Synced and Healthy.
- No old stack namespaces, workloads, APIs, gateways, RHOAI CSVs/subscriptions,
  enrollment policies or MaaS/enrollment PVCs/PVs remain.
- Shared routers available: Dagobah 2/2, site-e 1/1.
- Site-e's OpenShift operators Available and not Degraded.
- Dagobah's operators Available. Authentication remains Degraded due to the shared
  OAuth route certificate's unknown issuer. Its condition dates from
  **2026-10-04T17:01:52Z**, before this reset; tracked as `design-1xo.4`.
  Resolve it before new MaaS authentication acceptance.

Private resource snapshots and deletion/verification records are in
`~/.cache/dagobah-clean-slate-20261015/` on the operator workstation. They include
sensitive configuration and access credentials and must stay outside Git.
They are **not** database backups or a tested restore package. A Git revert cannot
recover the deleted database contents, invitations or site identities.

## Next rollout

Complete the digest-pinned payload and chart/CRD packaging, then follow
[the clean installation stages](rollout.md). Create fresh databases, Grid CA/site
identities and invitations. Install one platform/CRD owner, validate MaaS alone,
then enroll site-d and validate the MaaS-to-Grid trust contract before model traffic.
