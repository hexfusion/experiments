# PR 966 on the hub: published providers behind the MaaS gateway

Lab runbook for the MaaS-first layout test on dagobah (design repo
work/llm-d/ai-grid/GITOPS-V2.md, "Lab test payload"). Everything here sits beside the GitOps
payload: the grid definition carries the grid side, these files carry the RHOAI side and the
second consumer gateway, both applied by hand on 2026-10-10 and 2026-10-11.

## What the grid definition carries (grid.yaml, rendered by make render)

- The published provider in the hub's deriveTopology.fromProviders allowlist.
- enableProjectedCredentials and supportsProjectedCredentials on, since a bearer-token provider
  fails the render otherwise.
- mountReconciliation delegating the grid-consumer Deployment, with tlsCertMountPath outside
  the Helm config tree (/run/secrets/grid-identity).
- The backend CA bundle Secret (service CA plus lab CA) through the ACM policy in
  chart/templates/service-ca-secret.yaml and chart/files/ca/lab-ca.crt.
- The operator image with trusted private endpoints in the generated config.

## What is applied by hand, in order

1. Model controller image. Build opendatahub-io/odh-model-controller PR 966 with its own
   Dockerfile and push it (the robot login cannot create a quay repository, so it lives as a
   tag under grid-operator). Point the RHOAI operator CSV at it: omc-csv-image.sh. OLM carries
   the related image to the operator, which deletes and recreates the Deployment.
2. GRID environment on the model controller: omc-grid-env.sh. Run it again after any ODH
   operator restart, since the kserve module recreates the Deployment and 3.5.1 ignores the
   managed=false annotation.
3. Permissions the main branch needs and 3.5.1 lacks: omc-grid-lab-rbac.yaml (APIServer TLS
   profile, InferenceProviders).
4. The grid identity the controller publishes for: grid-model-client.yaml (service account and
   token Secret in the grid namespace), plus the allowed label on the tenant namespaces:

       kubectl label ns ai-tenant-site-a ai-tenant-site-b grid.praxis.fast/allowed=true

5. Opt a model in:

       kubectl annotate llminferenceservice qwen3 -n ai-tenant-site-a grid.praxis.fast/enabled=true

   The provider appears within seconds, named from namespace, service and UID. Add that name
   to fromProviders in grid.yaml and push.
6. MaaS gateway certificates with the Service SAN: ../../maas/site-a/gateway-cert.yaml and
   site-b, cert-manager from the lab CA issuer. If the backend CA bundle Secret still shows one
   certificate, nudge ACM:

       kubectl annotate policy -n grid-enrollment grid-service-ca-hq policy.open-cluster-management.io/trigger-update=$(date +%s) --overwrite

7. Second consumer gateway on the generated config: grid-consumer-values.yaml with the
   praxis-gateway chart from the odh-gitops bundle, release grid-consumer in the grid namespace.
   Parked at zero replicas until the operator can render a config praxis starts; see the
   rollout gate note in GITOPS-V2.md. Request through it: grid-consumer-request.sh.

## Verify

    kubectl get inferenceprovider
    kubectl get gridnetwork grid -o jsonpath='{.status.consumerConfigStatus[0].message}'
    kubectl get gridnetwork grid -o jsonpath='{.status.mountReconciliationStatus[0]}'
    kubectl get cm -n grid grid-consumer-config -o jsonpath='{.data.praxis\.yaml}'
    kubectl get secret -n grid grid-service-ca -o jsonpath='{.data.ca\.crt}' | base64 -d | grep -c BEGIN
    bash tests/frontdoor-smoke.sh

## Rollback

- Model controller: omc-csv-image.sh with the original registry.redhat.io digest in
  omc-original.image, then delete the ClusterRole and binding from omc-grid-lab-rbac.yaml.
- Remove the annotation from the LLMInferenceService; the controller deletes the provider and
  its grants. Drop the name from fromProviders.
- helm uninstall grid-consumer -n grid.
- The gateway certificates stay; they are strictly better than the hand-applied self-signed
  ones they replaced.
