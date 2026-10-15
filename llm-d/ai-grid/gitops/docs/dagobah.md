# Dagobah rollout

Status: the old stack was removed on 2026-10-15 UTC. The manual
[aiGrid enablement check](aigrid-enablement-20261015.md) now has four established
Grid CRDs and an available platform-managed Grid operator, using documented
the rebuilt PR #4186 image and its standard component pins. All temporary RBAC
has been removed and reconciliation passed. The new Argo root remains empty.
Payload preparation remains in progress;
`payload.yaml` is not a deployable image lock.

## Clean baseline

See the [reset record](clean-slate-20261015.md) for deletion scope, evidence and
retained infrastructure. Site-a/site-b were MaaS tenants on Dagobah; site-d is
the imported k3s spoke. Site-e remains imported with its former retail stack removed.
All eight nodes and all three ACM registrations are healthy.

Shared logging, observability, ingress and prerequisite operators remain.
Dagobah has a pre-existing OAuth certificate trust warning to resolve before
authentication acceptance. New databases, tenant models and Grid identities must
be created; this is a clean installation, not a migration of preserved state.

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
directly to main. Keep workload sync manual during qualification.
Follow [the rollout](rollout.md).
