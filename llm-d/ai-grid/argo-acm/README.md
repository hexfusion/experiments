# AI Grid through ACM and Argo CD

One file, grid.yaml, describes the whole grid: the artifact, the grid settings, the hub, and
every site. `make render` turns it into everything Argo CD deploys. The hub mints each site's
invite, ACM copies it to the site, and Argo CD installs the operator there, which enrolls
itself. The hub hosts enrollment, so it does not enroll: grid-enrollment's `hubSite` issues
its identity straight from the Grid CA. The sites are hq, the hub (OpenShift 4.20 and ACM
2.15.8, ACM cluster local-cluster), factory (k3s, ACM cluster site-d), and retail (OpenShift
SNO, ACM cluster site-e). Exploratory, not yet tracked in Jira.

Every endpoint is a name in the acme.lab zone. grid.acme.lab is the front door and
enroll.acme.lab is enrollment. swim.<site>.acme.lab is each site's SWIM Service, and
gw.<site>.acme.lab each provider site's gateway. hq's gateway is reached only through the
front door. grid.yaml's `dns` section holds the zone and the one table
of pinned addresses, and `make render` derives every host, seed, and loadBalancerIP from it.
The hub's router certificate must cover *.acme.lab for the front door's Route.

Every cluster resolves the acme.lab zone through ACM Policies: the OpenShift DNS operator
forwards it on OpenShift sites, and a coredns-custom ConfigMap does on k3s.
`dns.forward.enabled: false` rolls both back.

## Router certificate

The ingressCert setting has cert-manager issue the hub router's default certificate from the lab CA,
for the apps domain and the acme.lab zone. An ACM Policy creates the Certificate in
openshift-ingress. It sets the default IngressController's spec.defaultCertificate only
after the Secret exists, and changes nothing else on it. The site log collectors trust the
lab CA from that Secret. Deleting the Policy leaves both in place. Roll back by hand:

```bash
oc -n openshift-ingress-operator patch ingresscontroller default --type=json \
  -p '[{"op":"remove","path":"/spec/defaultCertificate"}]'
```

Clients verify with the lab CA's public certificate (CN Lab CA, SHA-256 fingerprint
F3:B4:2B:DD:33:4F:8A:7F:DB:59:57:44:39:3F:0F:43:50:0F:55:C8:15:AF:55:F1:0A:77:98:A9:B5:D6:A3:5C). Fedora hosts in the lab already trust it at
/etc/pki/ca-trust/source/anchors/lab-ca.crt. Elsewhere, extract it from the hub:

```bash
oc -n openshift-ingress extract secret/lab-ingress-cert --keys=ca.crt --to=-
```

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

1. Import the cluster into ACM. It needs no grid labels or ClusterSet: the Placements pick
   clusters by name from ACM's global set. The site's key in grid.yaml must equal its
   ManagedCluster name, because the Policy fetches and names invites by cluster name.
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

## Demo Grafana

`demoGrafana` installs grafana-operator on hq and serves a Grafana at observe.acme.lab, apart
from ACM's Grafana. It reads hq's Thanos Querier, which holds hq's user-workload metrics at
the 5s scrape interval this sets for hq's gateway and operator. Annotations come from hq's
LokiStack. Recording rules in the grid namespace hold the one mapping from ACM clusters,
backends, and tenant namespaces to site names. The dashboards in chart/files/demo use
those rules, word titles, and 30s rate windows. Factory and retail appear through what hq's
front door measures of them, since only hq's metrics reach this Querier.

## Findings

- A k3s import pulls nothing until the MultiClusterHub has `imagePullSecret`. The import
  bundle is generated at import time; set the secret first.
- GitOpsCluster refuses a cluster with no API URL, and a k3s site reports none, so its
  ManagedCluster sets `managedClusterClientConfigs`.
- The hub SWIM Service is the one gossip seed. Sites dial swim.<hub>.acme.lab, and
  `dns.loadBalancers.<hub>.swim` pins the address behind it, so it must not move.
- A site's key in grid.yaml is its grid site name, and `cluster` names its ManagedCluster.
  Each remote site gets its own invite Policy and Placement, selecting that one cluster by
  name, so a token reaches only the cluster that runs its site.
- The invite token is never in Git. The Policy's hub template reads it from the hub at
  apply time; hub templates only read Secrets in the Policy's own namespace, so the
  Policy lives in grid-enrollment.
- rhai-on-openshift-chart renders grid-only on k3s with `operator.enabled: false`.
- odh-gitops publishes no chart artifacts; the registry.redhat.io artifact comes from
  downstream release pipelines.
- setup/argocd.yaml binds Argo CD's controller to cluster-admin. Lab only.
