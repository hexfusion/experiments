# Object ownership

Reviewed 2026-10-16 against this lab and the selected Grid source
`33bab67860eae24717155a0a61c4bab3be2f36fd`.

This is the ownership map for the Grid installation, model serving, and its ACM,
MaaS and GitOps integrations. It covers declared resources and the resources their
controllers generate. Unrelated OpenShift workloads and historical demo fixtures
are outside this inventory. Certificate ownership is listed below separately.

**Admin** means administrator-controlled desired configuration. Git holds that
configuration and Argo applies it, or an administrator invokes Helm. **Controller**
means machine-generated state maintained by the named component. A Helm template
does not continuously reconcile anything by itself. Argo applies Helm-rendered
objects without running a Helm release lifecycle.

Ownership here means the authoritative writer of a field. Kubernetes
`ownerReferences` describe garbage collection; they do not enforce write access.
`managedFields` records writers but is not, by itself, an API permission boundary.

## Upstream architecture references

[Grid draft PR #395](https://github.com/praxis-proxy/grid/pull/395) adds concise,
standardized CRD ownership pages and a producer/consumer diagram. Its reference
covers upstream APIs. The tables here record the selected lab payload and its
ODH, MaaS, and ACM integrations.

[Grid draft PR #396](https://github.com/praxis-proxy/grid/pull/396) records
certificate issuers, writers, validity, rotation owners, and consumer reload.
[Grid issue #397](https://github.com/praxis-proxy/grid/issues/397) tracks the proposed
site bootstrap gate: detect first-time initialization from GridNetwork, preserve
existing certificate Secrets, and create the network after validation. This
proposal is not implemented or deployed in the lab payload.

## Installation and Grid objects

| Object | Desired configuration owner | Generated state / machine writer |
|---|---|---|
| Git site/model values and payload pins | Admin | No controller rewrites Git values |
| Bootstrap `AppProject` and root `Application` | Admin, bootstrap Helm render | Argo writes Application status and applies its resources |
| Child `Application` objects | Admin through the applications chart; parent Argo app applies spec | Argo writes sync, health and operation status |
| `DSCInitialization`, `DataScienceCluster` | Admin | ODH Platform operator writes status and creates component resources |
| Component CRs such as `AIGateway` and `Kserve` | ODH Platform operator from DSC intent | Component operators reconcile their workloads and status |
| Grid CRDs and operator Deployment, Services, ServiceAccounts and RBAC on Dagobah | AIGateway operator through the selected ODH payload | Kubernetes workload controllers write runtime status |
| Standalone Grid operator installation on a non-ODH site | Admin through `grid-operator` Helm values | Helm/Argo applies installation objects; Kubernetes runs them. This path must not compete with ODH ownership on Dagobah |
| `GridNetwork.spec`, including `tls`, `gatewayRefs`, region and peer settings | **Admin today**, through a chart or manifest | Grid operator reads these fields; it does not currently backfill missing TLS references from discovered Secrets |
| `GridNetwork.status` and Grid withdrawal finalizer | Grid operator | Network reconciler and the operator's specialized status paths update their fields |
| Explicit local `GridSite.spec` and provider-selection labels | Admin through site configuration | Grid operator writes observed readiness, membership, capabilities and certificate status |
| Automatically discovered peer `GridSite` objects | Grid operator, when discovery is configured | Grid operator creates/updates discovered objects and observed peer state. Do not apply a competing admin spec to the same object |
| `InferenceProvider` published from an `LLMInferenceService` | **ODH Model Controller** writes spec, source metadata and publication lifecycle | Grid operator writes provider status/readiness; neither controller owns the other's fields |
| Manually declared `InferenceProvider` | Admin through site/provider configuration | Grid operator writes status. Do not reuse an ODH-generated provider's identity |
| `AgentToolProvider` | Admin | Grid operator resolves endpoints/sites and writes status |
| Routing overlay and Grid serving ConfigMaps | Grid operator derives data from networks, sites, providers and signals | Grid operator maintains generated data; edit source intent instead of the ConfigMap |
| Generated consumer configuration and mount-requirements ConfigMaps | Grid operator, when consumer configuration is enabled | Grid operator writes the generated configuration and reports progress |
| Praxis gateway Deployment, Service, Route, ServiceAccount, RBAC, NetworkPolicy and chart ConfigMap | Admin through `praxis-gateway` values | Helm/Argo applies them; Kubernetes writes runtime status |
| Reserved mounts on an opted-in gateway Deployment | Grid operator only after explicit mount delegation and RBAC | Operator maintains the delegated volumes/mounts; Helm must release those fields. Other Deployment fields remain chart-owned |
| Gateway identity fingerprint annotation used for renewal rollout | Grid operator renewal path, when configured and authorized | Operator patches the named Deployment so renewed identity is loaded |
| Kubernetes ReplicaSets, Pods and EndpointSlices | Deployment/StatefulSet/Job and endpoint controllers | Controllers create/update generated objects; kubelet writes Pod status |

The deployed lab has `GridNetwork/dagobah-local` created by the local-models Argo
application. Its empty gateway references and null TLS references are incomplete
bootstrap configuration. Its generated `status.gridId` does not mean membership or
TLS setup is complete. Both generated Qwen InferenceProviders still have no matching
GridSites. Model Pod readiness and Grid integration readiness are separate checks.

## Enrollment objects and credentials

| Object / state | Owner | How it is written and maintained |
|---|---|---|
| Enrollment Deployment, Service, Route and RBAC | Admin configuration | Enrollment Helm chart; Argo or Helm applies rendered resources |
| Enrollment CA bootstrap Job and its RBAC | Admin configuration | Chart hook runs on install/upgrade; Job executes bootstrap and writes generated Secrets. This is not a continuously running issuance controller |
| Enrollment built-in Postgres Deployment, Service and PVC | Admin configuration | Enrollment chart declares them; Kubernetes/storage controllers manage runtime and volume binding. DB data has a separate retention requirement |
| Built-in DB password and connection Secret; optional local admin-token Secret | Bootstrap machine writer | Created when absent and retained; bootstrap does not continuously rotate these credentials |
| External DB connection Secret and trust bundle | Admin / external database integration | Supplied by the selected secret-management or database owner |
| Invite Job and requested site list | Admin configuration | Chart Job asks enrollment to mint an invite and writes its delivery Secret |
| Invite records, enrollment records, identity history and CA fingerprint in Postgres | Enrollment service | Transactional service-owned state, not Kubernetes spec or Git values |
| Kubernetes CSR enrollment audit records | Enrollment service when enabled | Service creates/approves/completes the record; the record is not the source of the site private key |
| Site invite token Secret on a joining cluster | Admin-authorized delivery, optionally ACM Policy | ACM configuration-policy controller writes the delivered copy when enabled; Grid operator consumes it |
| Joined site's identity Secret and CA trust Secret | Grid operator enrollment client | Operator generates a local key/CSR; enrollment signs it; operator writes the resulting identity and public CA bundle |
| Hub site's identity, CA copy and registration seed | Enrollment bootstrap Job | Enabled by `hubSite.name`; bootstrap issues the local identity and registers its key through the seed |
| Shared SWIM key Secret | Bootstrap / explicit administrator provisioning | Enrollment bootstrap can create it once; ACM can distribute it. It is an encryption key, not a certificate |
| `SiteInvite` CR | Not part of the currently deployed Grid CRD set | Do not describe it as a live owner; qualify it with the payload that adds the API/controller |

The enrollment host is explicitly selected through deployment intent. Today,
`grid-enrollment.hubSite.name` enables its local identity bootstrap. A joining site
uses `grid-operator.enrollment.enabled`, URL, trust bundle and invite. ACM hub status
does not automatically designate a Grid enrollment host. The current lab has no
running enrollment service and uses manually provisioned canary identities.

## Models, gateways and MaaS

| Object | Desired configuration owner | Generated state / machine writer |
|---|---|---|
| `qwen-serving`, `qwen-local-a`, `qwen-local-b` Namespaces and placement labels | Admin through local-models chart | Argo applies them |
| `LLMInferenceService.spec` | Admin through model definition and site placement | KServe LLMInferenceService controller writes status and manages generated serving resources |
| Platform `LLMInferenceServiceConfig` presets | Platform/KServe payload | Custom presets require a distinct admin-owned name; do not edit packaged presets in place |
| Model workload Deployment or LeaderWorkerSet; EPP Deployment and associated Services, ConfigMaps and ServiceAccounts | KServe LLMInferenceService controller | Kubernetes/LWS controllers generate Pods; KServe derives configuration from the LLMI service and presets |
| Model `InferencePool`, generated `HTTPRoute` and associated backend policy | KServe LLMInferenceService controller | Gateway/EPP controllers consume these and write the status fields they own |
| `Gateway/qwen-serving` and `ConfigMap/qwen-serving-config` | Admin through local-models chart | Istio Gateway API controller generates gateway Deployment/Service and writes Gateway status |
| Shared platform and MaaS Gateways, GatewayClasses and generated routing/filter resources | Platform/AIGateway controllers from their declared configuration | Istio and AI Gateway controllers reconcile their respective resources; manual pod patches are outside that desired-state path |
| `ServiceAccount/grid-model-client` and token Secret envelope | Admin through local-models chart | Kubernetes token controller writes service-account token data; the chart supplies no token bytes |
| `grid-model-credential-reader` Role/RoleBinding | Admin through local-models chart | Grants the Grid operator access to the named credential Secret |
| Per-model invocation Role/RoleBinding created during provider publication | ODH Model Controller | Controller updates and removes its publication grants with the source service/gateway lifecycle |
| `ExternalProvider/grid` and `ExternalModel/qwen3-coder-30b-a3b` | Admin; currently manual MaaS inputs | AI Gateway integration reconciles derived routes/processor configuration and status |
| `MaaSModelRef`, `MaaSAuthPolicy`, `MaaSSubscription` | Admin/API intent; currently manual lab inputs | MaaS controller reconciles status and derived policy resources |
| Derived Kuadrant `AuthPolicy` / rate-limit policy and auth-service resources | MaaS and Kuadrant controllers, according to their layer | Admin changes source MaaS configuration; generated policies are controller output |
| Provider API keys and hop-validation credentials | Admin / credential issuer | Referenced Secrets supply credentials; Grid does not originate external provider API keys |
| Existing site-d Qwen Deployment and Service | Admin, currently manual manifests | Kubernetes runs them. They are not generated by an LLMInferenceService in this lab |

Ownership gaps in this area are listed in [gaps](gaps.md#gitops-ownership).

## ACM, monitoring and logging

| Object | Desired configuration owner | Generated state / machine writer |
|---|---|---|
| `ManagedCluster` acceptance/labels and cluster-set membership | Admin through ACM onboarding/configuration | ACM registration/cluster controllers report joined/available status |
| `Placement`, `PlacementBinding`, `ManagedClusterSetBinding`, parent `Policy` | Admin through GitOps | ACM placement writes PlacementDecisions; governance controllers generate distributed policies and compliance status |
| Resources embedded in an enforced `ConfigurationPolicy` | Admin intent in the parent policy | ACM configuration-policy controller is the target-cluster writer; Argo must not independently own the same target fields |
| `ManifestWork` used by ACM delivery | ACM or the explicitly selected distribution controller | Managed-cluster work agent applies delivered resources and reports status |
| `MultiClusterObservability/observability` | Admin through GitOps | ACM observability operator creates/maintains Thanos, Grafana and collector resources |
| `observability-metrics-custom-allowlist` and dashboard ConfigMaps | Admin through GitOps | ACM consumes them; it does not decide which application metrics the administrator intends to retain |
| Application `ServiceMonitor`, `PodMonitor`, `PrometheusRule` | Their installing chart/controller, or explicit admin monitoring configuration | Prometheus Operator consumes them and generates monitoring runtime configuration. Do not add a second writer to a generated monitor |
| MinIO Deployment, Service, PVC and bootstrap Jobs/RBAC | Admin through observability GitOps | Jobs create storage credentials/buckets; Kubernetes runs the workload and binds storage |
| MinIO/Loki object-store credential Secrets | Named bootstrap Job | Generated data is machine-owned; Git supplies references and Job configuration |
| Logging `Subscription` and `OperatorGroup` | Admin through GitOps | OLM installs operators and writes installation status |
| `LokiStack`, `ClusterLogForwarder`, logging `UIPlugin` | Admin through GitOps | Logging/Loki/UI operators reconcile their generated resources |
| Remote Vector resources, log-writer token and CA delivery | Admin intent through logging ACM policies | ACM writes remote resources; Kubernetes token controller supplies token data; logging agents consume them |
| ACM agent/collector client certificates and platform monitoring TLS | ACM/OpenShift controllers | Platform-managed lifecycle, independent of Grid enrollment; importing a cluster does not issue a Grid site identity |

## Certificates and trust material

"Automatic" below names the trigger. Automatic initial creation does not imply
continuous renewal, and a changed Secret does not prove its consumer reloaded it.

| Certificate or trust object | Creator / issuer | Automatic trigger and ongoing owner |
|---|---|---|
| Enrollment `grid-ca-key` signing certificate/key | Enrollment bootstrap, or admin-provided CA | Built-in method creates it when absent on install/upgrade and preserves it. CA replacement is an explicit admin operation; not periodic renewal |
| Enrollment `grid-ca-bundle` and local `grid-ca` public copies | Enrollment bootstrap/client; ACM for delivered copies | Derived from the selected CA. Consumers receive the public bundle; the enrollment signing key is not distributed to joining sites |
| `enrollment-serving-tls` | Bootstrap signs the endpoint certificate with the Grid CA, unless provided by admin | Install/upgrade checks missing, invalid, SAN-changed or near-expiry material; renewal window is 30 days. Enrollment reloads mounted serving material every 60 seconds. No scheduled bootstrap renewal Job is supplied |
| `grid-db-serving-tls` for built-in Postgres | Enrollment bootstrap / Grid CA | Same bootstrap renewal checks; bootstrap rolls the named DB Deployment after a changed certificate |
| External enrollment DB certificate and CA ConfigMap | External database/PKI owner | Owner must arrange renewal; Grid consumes the bundle with hostname-verified TLS |
| Hub `grid-site-identity` | Enrollment bootstrap / Grid CA | `hubSite.name` triggers initial issuance. Existing matching identity is retained even if expired; configured operator enrollment renewal is a separate path |
| Joined-site `grid-site-identity` | Operator generates private key and CSR; enrollment service signs | Enrollment runs when enabled and identity absent. Renewal generates a new key at two-thirds of certificate lifetime. It requires valid current identity, reachable enrollment, enabled rotation and compatible peer trust |
| Identity renewal and peer pins | Grid operator plus enrollment service | Automatic identity rotation is disabled in `pin` trust mode. Admin coordinates re-enrollment and pin changes. `spiffe` mode allows the configured renewal path; default issued lifetime is 180 days |
| Standalone fallback CA/site Secrets referenced by `GridNetwork.spec.tls` | Grid network reconciler | Only runs when both references exist and both Secrets are absent. Creates an independent self-signed CA/site pair; does not replace an existing CA. This is not joining an established Grid |
| Current `grid/grid-canary-ca`, `grid/grid-canary-identity`; site-d `grid-canary/grid-canary-*` | Manually provisioned lab material | Live metadata shows `kubectl-client-side-apply`, no controller owner reference. Original generator is not recorded in the canonical configuration. No automatic enrollment/renewal owner is established |
| Praxis gateway inbound listener `grid/grid-gateway-tls` | OpenShift service CA operator | Service annotation requests issuance; service CA manages renewal. Gateway chart mounts it. Runtime reload/rollout must be qualified separately |
| Gateway outbound/Grid peer identity | Existing Grid site identity | Gateway consumes it. Configured Grid renewal can roll the named gateway Deployment; requires matching name/namespace and RBAC. The current canary identity is manually managed |
| Grid operator metrics `opendatahub/grid-operator-metrics-tls` | OpenShift service CA operator | Annotated metrics Service requests issuance/renewal; operator metrics listener reloads material. Standalone chart also supports site identity or admin-provided Secret |
| `openshift-ingress/qwen-serving-tls`, `maas-gw-service-tls`, `data-science-gateway-service-tls`, `kube-auth-proxy-tls` | OpenShift service CA operator | Annotated Services request creation/renewal; each gateway/proxy consumes its selected Secret |
| `openshift-ingress/grid-upstream-ca`, `openshift-service-ca.crt` ConfigMaps | Admin/controller creates annotated envelope; OpenShift service CA writes bundle data | CA injection is automatic. Mounting the bundle into a consumer is a separate gateway/deployment configuration responsibility |
| Qwen `qwen3-coder-30b-a3b-kserve-self-signed-certs` in each model namespace | KServe LLMInferenceService controller | Creates and reconciles the LLMI-owned Secret. In the selected ODH distro code it signs with the OpenShift service CA; the legacy Secret name alone does not establish self-signing. Consumer TLS refresh is a separate runtime check |
| KServe/LLMI webhook; ODH Model Controller webhook/metrics; model-serving API; MaaS webhook/metrics; AI Gateway operator metrics TLS Secrets | OpenShift service CA operator | Component Services request certificates; service CA writes/renews their data. Component controllers own consuming workload configuration |
| Istio `openshift-ingress/istio-ca-secret` and mesh workload certificates | Istiod / mesh certificate machinery | Mesh-managed identities and lifecycle; separate from Grid CA and gateway listener certificates |
| `cert-manager/lab-ca-secret`, `openshift-ingress/lab-ingress-cert` | cert-manager via `Certificate/lab-ca`, `Certificate/lab-ingress-cert` and selected Issuers | cert-manager reconciles Certificates and renewal. Admin owns issuer/trust design and CA distribution; changing a CA does not itself coordinate every consumer |
| Logging `grid-logging-ingress-ca` copies | ACM policy copies the selected ingress trust bundle | Policy-driven distribution; no new signing authority is created |
| User-supplied listener certs, upstream CA bundles, enrollment serving cert or Grid CA | Admin / selected external PKI integration | `existingSecret` or provided-CA options select consumption, not automatic issuance by Grid. The supplying integration owns renewal |
| Test certificates in Grid `xtask`/certificate tests and standalone Praxis probes | Test harness | Generated for the test run or provided as fixtures; no production renewal owner. The lab Praxis repro generates a one-day self-signed test certificate |

SWIM keys, invite tokens, service-account tokens, API keys and database passwords
are credentials, not certificates. Their writers are listed above so they are not
mistaken for CA or certificate lifecycle state.

Live ownership checks on 2026-10-16 confirmed service-CA writers for the listed
OpenShift serving Secrets, the LLMI owner reference on the Qwen TLS Secret, the
manual canary writers on both sites, and cert-manager ownership of the ingress
certificate. These are metadata/source checks, not an end-to-end rotation test.

## What an administrator can change

An admin-owned spec is the supported configuration surface. An edit to generated
controller-owned fields may be accepted by Kubernetes if permissions allow it,
but the controller can restore its desired value. Reconciliation does not prohibit
the write; admission or RBAC would be needed to reject it.

For Argo-owned input, an out-of-band change can survive until the next sync.
Self-heal makes that correction automatic; manual-sync Applications do not promise
immediate correction. Do not describe either behavior as a universal timer.

Controllers should respond to relevant changes and use periodic reconciliation as
a fallback, writing only when desired and observed state differ. Spec, status,
generated Secret data and delegated Deployment fields must have named writers.
Sharing an object is valid only with an explicit field boundary.

## API boundary still to decide

`GridNetwork` currently combines administrator network intent with references to
local site identity material. It is not the complete operator deployment config.
Operator image, RBAC, ports and enrollment settings live in a separate installation
configuration. The ownership map above describes existing behavior, not approval
of that API boundary.

If GridNetwork becomes controller-generated, define its authoritative input and
which fields the controller continuously maintains before changing the writer.
If it remains admin-authored, expose a coherent intent surface and keep derived
state in controller-owned fields/resources. Do not let Argo and a bootstrap
controller independently rewrite the same `spec.tls` fields.

Today enrollment may write default identity Secret names without a GridNetwork,
but it does not attach those Secrets by patching the network. The network
controller watches networks/providers/sites, not Secrets directly. Explicit
references let later reconciliation read the material. Automatic discovery and
attachment would require a deliberate network/site association and ownership
contract. Missing material must not be used as the signal to create a new Grid CA.

## Source references

- [Grid network reconciliation, including TLS initialization](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/operator/src/controller/grid_network.rs#L2017).
- [Grid network watches](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/operator/src/main.rs#L1142), [enrollment Secret selection](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/operator/src/enroll.rs#L301).
- [Enrollment chart inputs](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/charts/grid-enrollment/values.yaml), [bootstrap implementation](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/enrollment/src/bootstrap.rs), [serving reload](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/enrollment/src/main.rs).
- [Site renewal and gateway rollout](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/operator/src/enroll/renew.rs), [metrics TLS reload](https://github.com/opendatahub-io/praxis-ai-grid-operator/blob/33bab67860eae24717155a0a61c4bab3be2f36fd/crates/operator/src/metrics_tls.rs).
- [Selected KServe workload certificate issuer](https://github.com/opendatahub-io/kserve/blob/db12bf00b8d97e23bbed2ba02a75715bf97776f4/pkg/controller/v1alpha2/llmisvc/workload_tls_cert_odh.go), [workload Secret lifecycle](https://github.com/opendatahub-io/kserve/blob/db12bf00b8d97e23bbed2ba02a75715bf97776f4/pkg/controller/v1alpha2/llmisvc/workload_tls_self_signed.go).
- [OpenShift service CA responsibilities](https://github.com/openshift/service-ca-operator/blob/main/README.md), [cert-manager Certificate lifecycle](https://cert-manager.io/docs/usage/certificate/).
- Repository inputs: [site configuration](../sites/README.md), [local-model chart](../charts/local-models/templates/), [ACM/logging configuration](../observability/README.md), [payload differences](payload-diff.md).
