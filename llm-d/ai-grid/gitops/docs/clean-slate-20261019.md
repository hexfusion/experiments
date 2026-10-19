# Clean slate: 2026-10-19 UTC

The owner authorized tearing down the Dagobah stack so it can be reinstalled from
published ODH images with no custom carries. The new payload has not been installed.
This reset destroyed the Grid CA, site identities and the SWIM key; a clean install
mints fresh ones. A Git revert cannot recover them.

## Scope

Everything: Grid, enrollment and MaaS. Site-e and site-d retain their ACM registration.
Site-d is unreachable and has no remnants to remove. Site-e still runs an orphaned Grid
gateway; see Known remaining below.

## Order that worked

Custom resources were removed before their controllers, so finalizers cleared instead
of wedging. Argo automated sync was stopped first, and Applications carried no
`resources-finalizer.argocd.argoproj.io`, so deleting them orphaned their resources
rather than cascading. Orphans were then removed explicitly.

1. Stopped automated sync on `dagobah-grid-gateway`, `site-e-grid-gateway` and
   `site-d-grid-gateway`. Left `grid-logging` and `grid-observability` automated.
2. Deleted those three Applications plus `dagobah-local-models`. Retained the
   `dagobah` root.
3. Deleted 7 InferenceProviders, 2 GridSites, 1 GridNetwork, 7 LLMInferenceServices
   and the MaaS subscription, auth policy, model reference and tenant.
4. Deleted the DataScienceCluster, then the DSCInitialization. Component CRs went to
   zero and the platform removed every component controller.
5. Deleted the platform operator's validating and mutating webhook configurations,
   then 16 namespaces.
6. Deleted 32 CRDs, selected by API group.
7. Removed the stack's workloads and configuration from the shared `openshift-ingress`
   namespace: 2 Gateways, the 2 orphaned ExtProc Deployments and their Services,
   4 EnvoyFilters, 3 ConfigMaps, 1 ServiceAccount and 1 ClusterRoleBinding.
8. Removed the `maas-gateway-class` GatewayClass and the `maas-gateway-route` Route.

Deleting the two Gateways left no Gateway referencing the Service Mesh gateway control
plane, so `IstioRevision/openshift-gateway` and its `istiod-openshift-gateway` Deployment
were garbage collected. That matches the previous reset, where the RHOAI-managed gateway
control planes went and the operators stayed. `servicemeshoperator3.v3.3.1` is still
installed and the five Gateway API CRDs are intact, so a reinstall can recreate it.

Enrollment needed no separate step. Its Deployment, database and identities went with the
`grid-enrollment` and `grid-identity` namespaces, and the enrollment ACM policies had
already been removed in the previous reset and never recreated. No Grid CertificateSigningRequest,
Grid PersistentVolume or enrollment policy remains.

MaaS needed no separate step beyond the GatewayClass and Route. Its controllers and API
went with the `opendatahub` namespace, its tenants and configuration with
`models-as-a-service`, `ai-tenants` and `odh-ai-gateway-infra`, and its eight
`maas.opendatahub.io` CRDs plus the two `inference.opendatahub.io` CRDs with the CRD pass.
Kuadrant is retained: it is shared substrate, the previous reset kept it, and no MaaS
AuthPolicy or RateLimitPolicy is left on it. Removing it would mean reinstalling operators,
which the rollout stages do not ask for.

## Two naming traps

Selecting by name would have deleted the wrong objects twice.

`openstackservers.infrastructure.cluster.x-k8s.io` matches a case-insensitive search
for `kserve`, because "openstac-kserve-rs" contains it. CRDs were therefore selected by
API group, which produced 32 names and retained 139 other groups.

`cluster-logging-openshift-logging-grid-metrics-auth` matches a search for
`grid-metrics` but belongs to the retained `grid-logging` Application. Only the
`payload-processing` RBAC was removed.

## Teardown-order lesson

The AITenant wedged on `maas.opendatahub.io/aitenant-cleanup` because its API-key
revocation Job returned 401. The MaaSSubscription and MaaSAuthPolicy had already been
deleted, which plausibly removed the authorization that Job uses. Read this as an
ordering mistake, not a defect: delete the AITenant before its subscription and auth
policy, or accept clearing the finalizer when the key store is being destroyed anyway.

## Retained

All nodes, the three ACM managed clusters and their registrations. The `dagobah` root
with manual sync, and the `grid-logging` and `grid-observability` Applications, both
Synced and Healthy, with their `grid-observability-minio` storage. The `site-d` and
`site-e` namespaces, which are ACM ManagedCluster namespaces rather than stack
namespaces. OpenShift ingress `router-default` and the Service Mesh
`istiod-openshift-gateway` control plane. ACM, GitOps, Service Mesh, Kuadrant,
cert-manager, MetalLB, GPU and networking operators.

## Verification

Six nodes Ready. `local-cluster`, `site-d` and `site-e` joined and available. No stack
namespace, CRD, workload, Route, Gateway, GatewayClass, EnvoyFilter or Grid certificate
remains anywhere on the hub, and no workload anywhere runs a custom image. Every
cluster operator Available and not Degraded, with one exception: `authentication` has
been Degraded since 2026-10-04T17:01:52Z on the shared OAuth route certificate's
unknown issuer. That predates this reset and the previous one, and is tracked as
`design-1xo.4`. Resolve it before MaaS authentication acceptance.

Snapshots are in `~/.cache/dagobah-clean-slate-20261019/` on the operator workstation,
outside Git because they carry configuration and access material. They include the
pre-teardown EnvoyFilters, which record the body modes the routes declared.

## Known remaining

Site-e runs an orphaned Grid gateway. Its Application is gone and its hub GridSite
record and identity are destroyed, so it is inert. Removing it needs site-e
credentials, which this session does not hold. Handle it when site-e is re-enrolled.

Site-d answers neither its API nor a route to its address, and holds no Grid namespace
that this session could observe. It stays ACM-joined and needs nothing.

Kuadrant and its Authorino and Limitador instances remain in `kuadrant-system`, idle.

## Next

Install from the ODH-only selection in `payload/selection.yaml`, which pins the
published platform operator containing PR 4186 and takes the Grid images from the
midstream `d402270` build. The Grid layer needs no overrides. Then run the Grid front
door smoke, then the MaaS gate at `tests/maas-extproc/reproduce.py --expect fixed`.
