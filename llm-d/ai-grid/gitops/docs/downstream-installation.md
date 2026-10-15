# AI Grid with MaaS: downstream installation draft

Audience: downstream installation documentation authors and administrators validating
the emerging stack. This draft describes the intended workflow; it is not a claim
that the custom ODH lab payload is a released or supported RHOAI 3.6 installation.
Replace experimental source/image references with the released downstream payload
and its supported version matrix before publication.

## Intended arrangement

Clients enter through the MaaS gateway for authentication, model authorization and
usage limits. The internal Grid gateway selects a provider site. Provider traffic
uses the identity, trust and authorization required by that site's serving endpoint.
The hub hosts enrollment; provider sites enroll using their own identities. A k3s
provider does not imply support for installing the full RHOAI platform on k3s.

In a platform-managed installation, configure the DataScienceCluster; the platform
operator projects the AIGateway resource. Do not independently edit that child CR.
The platform passes component images to AIGateway. The exact MaaS API/controller
resource ownership must be checked against the selected payload's manifests.
AIGateway's aiGrid component installs the Grid operator and CRDs. The runtime charts
install gateway/enrollment workloads; do not also install a second Grid operator chart.

See [the portal and ACM flow](acm-gitops-flow.md) for the proposed Helm-values
provisioning path, component ownership and remaining integration work.

## Prerequisites and installation outline

1. Select a compatible platform payload, chart and component image set. Document
   required OpenShift/Kubernetes, Gateway API, Service Mesh, RHCL/Kuadrant,
   cert-manager and LWS versions using the eventual release's support matrix.
2. Decide which existing operators are externally managed. Disable corresponding
   chart dependency installation to prevent duplicate OLM owners. Reuse ACM's
   observability installation when it owns that dependency.
3. Provision DNS, TLS trust, persistent enrollment storage, and an enrollment-specific
   database if using external PostgreSQL. Its connection Secret uses the
   `DB_CONNECTION_URL` key. Keep credentials and CA private keys outside Git.
4. Install the platform and wait for APIs, then enable the required components in
   the DSC. The intended field shape is shown below; merge it into the complete
   desired DSC instead of replacing an existing installation with this fragment.
5. Configure MaaS tenants, model access, subscriptions and gateway policies using
   the selected MaaS version. Validate key authentication and limits.
6. Configure enrollment and a real Grid routing configuration, then deploy runtimes.
   Explicitly restrict network access to intended callers; default NetworkPolicies
   are not a substitute for a site-specific trust design.
7. Enroll provider sites, configure/publish providers and validate model routing.
   If the model-controller publishing patch is required, document its opt-in,
   namespace selector, identity provisioning and model-scoped permissions.

```yaml
spec:
  components:
    kserve:
      managementState: Managed
    aigateway:
      managementState: Managed
      modelsAsAService:
        managementState: Managed
      aiGrid:
        managementState: Managed
```

With the chart revision in this lab, `profile: ai-grid` enables AIGateway/aiGrid,
but other components remain Removed unless explicitly configured. Preserve existing
KServe and MaaS configuration when constructing final values. For custom images,
use supported related-image inputs and inspect the complete render.

## Lifecycle and verification

Validate current-generation component readiness and actual authorized inference.
Test invalid credentials, unauthorized models, rate limits, provider withdrawal,
network isolation, identity renewal and persistence across controller reconciliation.
Keep the acceptance evidence with the selected payload.

Setting aiGrid or its parent to Removed through Helm/GitOps suppresses the runtime
render and can remove resources on upgrade/prune. Editing the live DSC alone does
not reconcile separately Helm/Argo-owned runtime resources. Plan runtime removal
and operator removal together, preserving externally managed configuration, Secrets,
databases and required persistent state.

## Publication gaps

- Released downstream image/chart references and supported version matrix.
- Confirmed custom patch set and its replacement by released component builds.
- Tested MaaS database migration and rollback across the chosen versions.
- Validated clean installation and upgrade procedures, with exact configuration
  examples for hub and provider roles and reconciliation-safe publisher settings.
- Verified production handoff identity between MaaS and Grid. The lab's internal
  network restriction alone must not be presented as cryptographic authentication.

Source references: [platform PR 4186](https://github.com/opendatahub-io/opendatahub-operator/pull/4186),
[chart PR 181](https://github.com/opendatahub-io/odh-gitops/pull/181),
[model publisher PR 966](https://github.com/opendatahub-io/odh-model-controller/pull/966).
