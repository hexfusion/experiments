# Dagobah rollout

Status: root bootstrapped; payload preparation in progress. No platform, MaaS, Grid, or model workload has been changed
by this new root. `payload.yaml` is an incomplete selection record, not an image lock.

## Baseline observed on 2026-10-15 UTC

- Context: `default/api-dagobah-hexfusion-local:6443/kube:admin`.
- Six Ready nodes, Kubernetes v1.33.10.
- `redhat-ods-operator/rhods-operator` Subscription: RHOAI 3.5.1,
  `stable-3.5`, `redhat-operators`, automatic InstallPlan approval.
- `default-dsc`: KServe and AIGateway/MaaS Managed; aiGrid absent.
- Existing Helm release metadata: `rhoai` in namespace `grid`.
- Existing Argo root: `openshift-gitops/ai-grid`, sourced from
  `llm-d/ai-grid/argo-acm/apps` on `main`.
- `ai-grid`, `grid-hq`, and `grid-hq-crds` OutOfSync; hub Grid apps use manual
  sync. Enrollment, factory, retail and front-door apps have automatic sync.
- ACM clusters: `local-cluster`, `site-d` (k3s), `site-e` (OpenShift).
  `site-a` and `site-b` are currently tenant namespaces on Dagobah, not imported
  ManagedClusters. Keep that distinction when designing the three-site layout.
- Grid runs in `grid`; enrollment and its database run in `grid-enrollment`.
  The hub gateway/operator and model-controller use custom lab images.
- MaaS API instances and PostgreSQL run in `redhat-ai-gateway-infra`;
  maas-controller runs in `redhat-ods-applications`. Existing site-a/site-b MaaS
  Gateways and the default MaaS Gateway run in `openshift-ingress`.

## Bootstrap the separate root

The initial source contains zero child Applications. Bootstrapping creates the
root/project only; it does not deploy the new stack or migrate existing resources.
The separate root/project were bootstrapped on 2026-10-15 UTC. To reproduce:

From the repository root:

```bash
set -o pipefail
helm template grid-bootstrap llm-d/ai-grid/gitops/bootstrap \
  -f llm-d/ai-grid/gitops/values/lab.yaml | \
  kubectl --context default/api-dagobah-hexfusion-local:6443/kube:admin apply --dry-run=server -f -
helm template grid-bootstrap llm-d/ai-grid/gitops/bootstrap \
  -f llm-d/ai-grid/gitops/values/lab.yaml | \
  kubectl --context default/api-dagobah-hexfusion-local:6443/kube:admin apply -f -
kubectl get applications.argoproj.io dagobah -n openshift-gitops
```

Before adding children, create workload AppProjects scoped to their required
repositories, clusters, namespaces and resource kinds. The bootstrap project only
allows Applications in `openshift-gitops`; it is not a workload project. Review
child project changes as privileged configuration.

The root tracks `main` and renders the shared Application chart with
`values/lab.yaml`. Changes to this experiments configuration can be pushed
directly to main. Keep workload sync manual during the ownership handoff.
Follow [the rollout](rollout.md).
