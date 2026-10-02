# AI Grid through ACM and Argo CD

Argo CD deploys the whole grid from this directory. The hub mints each site's invite,
ACM copies it to the site, and Argo CD installs the operator there, which enrolls itself.
The hub hosts enrollment, so it does not enroll: grid-enrollment's `hubSite` issues its
identity straight from the Grid CA, and its operator runs with enrollment off.
Hub: dagobah (OpenShift 4.20, ACM 2.15.8). Site: site-d (k3s). Exploratory, not yet
tracked in Jira.

## Layout

| Path | Applied by | What it does |
|---|---|---|
| setup/01-gitops-operator.yaml | you, once | OpenShift GitOps operator |
| setup/02-managedcluster-site-d.yaml | you, once | site-d as a ManagedCluster in the grid set, with its API URL |
| bootstrap.yaml | you, once | Argo CD admin binding and the `ai-grid` app of apps |
| apps/01-acm.yaml | Argo CD | grid ClusterSet, Placement, GitOpsCluster |
| apps/01-grid-namespace.yaml | Argo CD | the hub's `grid` namespace, before enrollment writes the hub identity into it |
| apps/02-enrollment.yaml | Argo CD | the rendered hub/enrollment manifests: the authority, the hub identity and SWIM key, and one invite per site |
| apps/03-invite-policy.yaml | Argo CD | ACM Policy copying each site's invite, the Grid CA bundle, and the SWIM key to the site, skipping the hub (ACM's `local-cluster=true`) |
| apps/04-applicationset.yaml | Argo CD | one app per sites/<site>/site.yaml, deploying that site's rendered manifests to the cluster it names |
| chart.env | `make render` | the one chart and version every manifest is rendered from |
| values/common.yaml | `make render` | values every site shares: images, grid settings, the SWIM Service, the gateway identity |
| sites/<site>/values.yaml | `make render` | the site: name, region, zone, enrollment on or off, its models, and its gateway |
| sites/<site>/site.yaml | Argo CD, `make render` | the Argo CD cluster the site deploys to, and the APIs that cluster serves |
| sites/<site>/manifests/, hub/enrollment/manifests/ | Argo CD | rendered, never edited by hand |
| hub/enrollment/values.yaml, target.yaml | `make render` | the hub's enrollment values and cluster APIs |

The manifests are `helm template` output of `oci://quay.io/sbatsche/rhai-on-openshift-chart`
at the version in chart.env, with only the grid subcharts on (`operator.enabled: false`).
Argo CD applies them as plain directories. It still runs the charts' `helm.sh/hook` Jobs
(the CA bootstrap, the invites) as sync hooks, which Argo CD reads from the annotation
whatever the source type. Sync waves: hub cluster, ACM (-1), the grid namespace (0),
enrollment (1), policy (2), sites (3); in a site, the GridNetwork, GridSite, and
InferenceProviders sync a wave after the CRDs.

## Change a site

Edit the values (chart.env, values/common.yaml, sites/<site>/values.yaml, or
hub/enrollment/values.yaml) and push to main. The ai-grid-render workflow runs
`make render` and commits the regenerated manifests, and Argo CD syncs them. A pull request
that changes the manifests without the values fails `make check`. A gateway config change
renders a new ConfigMap and config checksum, so the gateway Deployment rolls. On that site:

```bash
kubectl get gridoperator cluster -o jsonpath='{range .status.conditions[?(@.type=="GatewayProgressing")]}{.status}/{.reason}: {.message}{"\n"}{end}'
# True/RollingOut: grid/grid-gateway rolling out: 1 of 2 pods updated ...
```

## Render a site locally

```bash
make render                      # every site and the hub, from the chart in chart.env
make check                       # fail if the committed manifests differ from a render
make render CHART=/tmp/rhai-on-openshift-chart-<version>.tgz   # from a package not pushed yet
```

It needs `helm` (3.17.3, the version the workflow pins) and `yq`. Each render deletes the
old output first, so a template the chart dropped disappears from manifests/.

## Charts

| Chart | Artifact |
|---|---|
| rhai-on-openshift-chart | `oci://quay.io/sbatsche/rhai-on-openshift-chart:3.6.0-aigrid.dev-14fb3fea` |

It is odh-gitops#181 with the grid subcharts vendored from hexfusion/grid. Build and push:

```bash
podman login quay.io
helm package charts/rhai-on-openshift-chart --version 3.6.0-aigrid.dev-<grid sha>  # in odh-gitops
helm push <chart>.tgz oci://quay.io/sbatsche --registry-config ${XDG_RUNTIME_DIR}/containers/auth.json
# then set VERSION in chart.env and make render
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
   oc label managedcluster local-cluster site=dagobah cluster.open-cluster-management.io/clusterset=grid --overwrite
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
hub/enrollment/values.yaml, keyed by its ManagedCluster name, and add
sites/<name>/site.yaml and values.yaml. The workflow renders it, and the ApplicationSet
picks it up by its site.yaml. The Policy fetches a cluster's token by its name, not its
labels, and encrypts the token and the SWIM key it copies. A consumer gateway lists one
backend per site it routes to.

The dagobah gateway is the grid front door, at `https://grid.apps.dagobah.hexfusion.local`
through a reencrypt Route to its service-ca listener cert, with MaaS API keys.

## Reset

Enrollment never releases a site name, so a repeat run needs a fresh authority:

```bash
oc delete application -n openshift-gitops ai-grid --cascade=foreground
oc delete ns grid-enrollment
oc delete secret -n grid grid-site-identity grid-ca   # the hub identity, from the old CA
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
