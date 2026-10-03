# AI Grid through ACM and Argo CD

One file, grid.yaml, describes the whole grid: the artifact, the grid settings, the hub, and
every site. `make render` turns it into everything Argo CD deploys. The hub mints each site's
invite, ACM copies it to the site, and Argo CD installs the operator there, which enrolls
itself. The hub hosts enrollment, so it does not enroll: grid-enrollment's `hubSite` issues
its identity straight from the Grid CA. Hub: dagobah (OpenShift 4.20, ACM 2.15.8). Site:
site-d (k3s). Exploratory, not yet tracked in Jira.

## Layout

| Path | Written by | What it is |
|---|---|---|
| grid.yaml | you | the whole grid |
| chart/ | you | grid-gitops, the chart stage 1 renders |
| Makefile | you | `make render` and `make check` |
| setup/gitops-operator.yaml, setup/argocd.yaml, setup/kustomization.yaml | you | the seed: GitOps operator, Argo CD controller memory and admin binding |
| setup/root-app.yaml | stage 1 | the `ai-grid` app of apps, applied once by the seed |
| apps/ | stage 1 | ACM objects, the hub's grid namespace, the enrollment app, the invite Policy, one app per site |
| values/common.yaml, sites/SITE/values.yaml, sites/SITE/site.yaml | stage 1 | each site's product-chart values and cluster APIs |
| hub/enrollment/values.yaml, target.yaml | stage 1 | the enrollment values: hubSite, one invite per non-hub site |
| sites/SITE/manifests/, hub/enrollment/manifests/ | stage 2 | `helm template` of the product chart in grid.yaml's artifact |

Argo CD applies apps/ and the manifests as plain directories. It still runs the charts'
`helm.sh/hook` Jobs (the CA bootstrap, the invites) as sync hooks. Sync waves: ACM (-1),
the grid namespace (0), enrollment (1), the Policy (2), sites (3). Within a site, the
GridNetwork, GridSite, and InferenceProviders sync a wave after the CRDs.

## Render

```bash
make render                      # stage 1 from grid.yaml, then stage 2 from the chart it names
make check                       # fail if any generated file differs from a render
make render CHART=/tmp/rhai-on-openshift-chart-<version>.tgz   # from a package not pushed yet
```

It needs `helm` (3.17.3, the version the workflow pins) and `yq`. Each render deletes the
old output first, so a site removed from grid.yaml disappears. On main, the ai-grid-render
workflow renders and commits the output of a grid.yaml change. On a pull request,
`make check` fails generated output that does not match.

## Add a site

1. Import the cluster into ACM with labels `site=<name>` and
   `cluster.open-cluster-management.io/clusterset=grid`. The site's key in grid.yaml must
   equal its ManagedCluster name, because the Policy fetches invites by cluster name.
2. Add an entry under `sites:` in grid.yaml: cluster, region, zone, apiVersions, providers,
   gateway.
3. `make render`, commit, push.

Stage 1 adds the site's invite, its Argo CD app, and its values. Stage 2 adds its manifests.

## Charts

| Chart | Artifact |
|---|---|
| rhai-on-openshift-chart | `artifact.chart` and `artifact.version` in grid.yaml |

It is odh-gitops#181 with the grid subcharts vendored from hexfusion/grid. Build and push:

```bash
podman login quay.io
helm package charts/rhai-on-openshift-chart --version 3.6.0-aigrid.dev-<grid sha>  # in odh-gitops
helm push <chart>.tgz oci://quay.io/sbatsche --registry-config ${XDG_RUNTIME_DIR}/containers/auth.json
# then set artifact.version in grid.yaml and make render
```

The quay repository must exist, with Write for the pushing account. Helm drops `:443`
from the host, so log in as `quay.io`.

## Findings

- A k3s import pulls nothing until the MultiClusterHub has `imagePullSecret`. The import
  bundle is generated at import time; set the secret first.
- GitOpsCluster refuses a cluster with no API URL, and a k3s site reports none, so its
  ManagedCluster sets `managedClusterClientConfigs`.
- The invite token is never in Git. The Policy's hub template reads it from the hub at
  apply time; hub templates only read Secrets in the Policy's own namespace, so the
  Policy lives in grid-enrollment.
- rhai-on-openshift-chart renders grid-only on k3s with `operator.enabled: false`.
- odh-gitops publishes no chart artifacts; the registry.redhat.io artifact comes from
  downstream release pipelines.
- setup/argocd.yaml binds Argo CD's controller to cluster-admin. Lab only.
