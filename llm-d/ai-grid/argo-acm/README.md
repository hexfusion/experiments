# AI Grid through ACM and Argo CD

Argo CD deploys the whole grid from this directory. The hub mints each site's invite,
ACM copies it to the site, and Argo CD installs the operator there, which enrolls itself.
Hub: dagobah (OpenShift 4.20, ACM 2.15.8). Site: site-d (k3s). Exploratory, not yet
tracked in Jira.

## Layout

| Path | Applied by | What it does |
|---|---|---|
| setup/01-gitops-operator.yaml | you, once | OpenShift GitOps operator |
| setup/02-managedcluster-site-d.yaml | you, once | site-d as a ManagedCluster in the grid set, with its API URL |
| bootstrap.yaml | you, once | Argo CD admin binding and the `ai-grid` app of apps |
| apps/00-repo-quay.yaml | Argo CD | quay.io/sbatsche as an OCI Helm repository |
| apps/00-hub-cluster.yaml | Argo CD | the hub itself as Argo cluster `dagobah` in the grid set |
| apps/01-acm.yaml | Argo CD | grid ClusterSet, Placement, GitOpsCluster |
| apps/02-enrollment.yaml | Argo CD | rhai-on-openshift-chart with grid-enrollment on: the authority and one invite per site |
| apps/03-invite-policy.yaml | Argo CD | ACM Policy copying each site's invite and the Grid CA bundle to the site |
| apps/04-applicationset.yaml | Argo CD | rhai-on-openshift-chart with grid-operator and praxis-gateway on, per grid site |
| sites/<site>.yaml | Argo CD | everything that differs per site: SWIM join address, enrollment URL, gateway role and routes |

Every chart comes from one artifact, `oci://quay.io/sbatsche/rhai-on-openshift-chart`,
with only the grid subcharts on (`operator.enabled: false`). Sites are named by the
ManagedCluster `site` label. Sync waves: repository, hub cluster, ACM (-1), enrollment
(1), policy (2), sites (3).

## Reconfigure a gateway

Gateway config is values in sites/<site>.yaml under `praxis-gateway.gatewayConfig`. Edit,
commit, push. Argo CD re-renders the gateway ConfigMap, the config checksum changes, and
the gateway Deployment rolls. On that site:

```bash
kubectl get gridoperator cluster -o jsonpath='{range .status.conditions[?(@.type=="GatewayProgressing")]}{.status}/{.reason}: {.message}{"\n"}{end}'
# True/RollingOut: grid/grid-gateway rolling out: 1 of 2 pods updated ...
```

Examples: add a backend under `backends`, change `auth.mode`, or allow another peer in
`peerTrust.spiffeIds`.

## Charts

| Chart | Artifact |
|---|---|
| rhai-on-openshift-chart | `oci://quay.io/sbatsche/rhai-on-openshift-chart:3.6.0-aigrid.dev-3ce1f498` |

Both are built from hexfusion/grid `rollup/operator-standalone`; the second is
odh-gitops#181 with its grid subcharts vendored from that branch. Build and push:

```bash
podman login quay.io
helm package charts/grid-enrollment --version 0.1.0-aigrid.a7f8d2ca              # in grid
helm package charts/rhai-on-openshift-chart --version 3.6.0-aigrid.dev-a7f8d2ca  # in odh-gitops
helm push <chart>.tgz oci://quay.io/sbatsche --registry-config ${XDG_RUNTIME_DIR}/containers/auth.json
helm show chart oci://quay.io/sbatsche/grid-enrollment --version 0.1.0-aigrid.a7f8d2ca
```

The quay repository must exist, with Write for the pushing account. Helm drops `:443`
from the host, so log in as `quay.io`.

## Deploy

1. Hub pull secret, before importing a k3s site:

   ```bash
   oc get secret -n openshift-config pull-secret -o json \
   | jq '{apiVersion,kind,type,data,metadata:{name:"acm-pull-secret",namespace:"open-cluster-management"}}' | oc apply -f -
   oc patch multiclusterhub multiclusterhub -n open-cluster-management --type merge -p '{"spec":{"imagePullSecret":"acm-pull-secret"}}'
   ```

2. GitOps operator and the site import:

   ```bash
   oc apply -f setup/
   oc get secret -n site-d site-d-import -o jsonpath='{.data.crds\.yaml}'   | base64 -d | kubectl --context site-d apply -f -
   oc get secret -n site-d site-d-import -o jsonpath='{.data.import\.yaml}' | base64 -d | kubectl --context site-d apply -f -
   oc get managedclusters
   ```

3. Put the hub in the grid. Sites are named by the ManagedCluster `site` label, so the
   hub's `local-cluster` becomes site `dagobah`:

   ```bash
   oc label managedcluster local-cluster site=dagobah grid=lab cluster.open-cluster-management.io/clusterset=grid --overwrite
   ```

4. Hand the grid to Argo CD:

   ```bash
   oc apply -f bootstrap.yaml
   ```

5. Watch:

   ```bash
   oc get applications -n openshift-gitops
   oc get policy -n grid-enrollment
   oc logs -n grid-enrollment deploy/grid-enrollment -f
   kubectl --context site-d get gridoperator cluster -o yaml
   ```

   Done when `grid-site-d` is Synced and Healthy, policy `grid-invite` is Compliant, and
   site-d's GridOperator says "enrolled as spiffe://grid.internal/site/site-d".

Adding a site: import it into ACM with labels `site=<name>` and
`cluster.open-cluster-management.io/clusterset=grid`, add it under `invites:` in
apps/02-enrollment.yaml, and add sites/<name>.yaml. The Policy and the ApplicationSet pick
it up by the `site` label.

## Reset

Enrollment never releases a site name, so a repeat run needs a fresh authority:

```bash
oc delete application -n openshift-gitops ai-grid --cascade=foreground
oc delete ns grid-enrollment
kubectl --context site-d delete ns grid
kubectl --context site-d get crd -o name | grep grid.praxis.fast | xargs kubectl --context site-d delete
```

## Findings

- A k3s import pulls nothing until the MultiClusterHub has `imagePullSecret`. The import
  bundle is generated at import time; set the secret first.
- GitOpsCluster refuses a cluster with no API URL, and a k3s site reports none, so
  setup/02 sets `managedClusterClientConfigs`.
- The invite token is never in Git. The Policy's hub template reads it from the hub at
  apply time; hub templates only read Secrets in the Policy's own namespace, so the
  Policy lives in grid-enrollment.
- rhai-on-openshift-chart renders grid-only on k3s with `operator.enabled: false`.
- odh-gitops publishes no chart artifacts; the registry.redhat.io artifact comes from
  downstream release pipelines.
- bootstrap.yaml binds Argo CD's controller to cluster-admin. Lab only.
