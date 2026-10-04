# AI Grid through ACM and Argo CD

grid.yaml describes the whole grid: the artifact, the grid settings, the hub and every site.
`make render` turns it into everything Argo CD deploys. The hub mints each site's invite,
ACM copies it to the site, and the site's operator enrolls itself. The hub hosts enrollment,
so it takes its identity straight from the Grid CA. sites/README.md says what each site does.
The build the demo runs is pinned in payload.yaml. Exploratory, not tracked in Jira.

## Names

Every endpoint is a name in the acme.lab zone: grid.acme.lab (front door, grid mode),
maas.acme.lab (front door, maas mode), enroll.acme.lab, gw.<site>.acme.lab and
swim.<site>.acme.lab. grid.yaml's `dns` section holds the zone and the pinned addresses.
ACM Policies forward the zone on every cluster; `dns.forward.enabled: false` rolls that back.
The hub router's certificate comes from the lab CA and covers *.acme.lab. Get the CA with:

```bash
oc -n openshift-ingress extract secret/lab-ingress-cert --keys=ca.crt --to=-
```

## Layout

| Path | Source | What it is |
|---|---|---|
| grid.yaml, payload.yaml | edited by hand | the whole grid, and the build it runs |
| chart/, Makefile | edited by hand | the stage 1 chart, and `make render` and `make check` |
| setup/ (except root-app.yaml) | edited by hand | the seed: GitOps operator and Argo CD |
| setup/root-app.yaml, apps/ | make render, stage 1 | the app of apps and every Argo CD app and ACM object |
| values/, sites/SITE/values.yaml, site.yaml, hub/enrollment/values.yaml | make render, stage 1 | product-chart values per site |
| sites/README.md | make render, stage 1 | each site's role, from grid.yaml |
| sites/SITE/manifests/, hub/enrollment/manifests/ | make render, stage 2 | `helm template` of the product chart |
| sites/SITE/crds/ | make render, stage 2 | the site's CRDs, moved out of manifests/ |

Argo CD applies apps/ and the manifests and crds directories, and runs the charts' hook Jobs
as sync hooks. Waves: ACM (-1), grid namespace (0), enrollment (1), invite Policy and site
CRDs (2), sites (3).

## Render

```bash
make render     # stage 1 from grid.yaml, then stage 2 from the chart it names
make check      # fail if any generated file differs from a fresh render, or payload.yaml drifts
```

It needs helm 3.17.3 and yq. A render deletes the old output first, so a site removed from
grid.yaml disappears. `OFFLINE=1 make check` skips the GitHub checks on payload.yaml.

## Add a site

1. Import the cluster into ACM. Its key in grid.yaml must equal its ManagedCluster name.
2. Add it under `sites:` in grid.yaml, with a `description`.
3. `make render`, commit and push.

## Payload

payload.yaml lists praxis-proxy/grid main at a fixed commit plus open PRs at their heads,
and nothing else. `make check` fails when grid.yaml's image digests differ from it, a listed
PR closed unmerged or moved, or the base left main. Build the chart from odh-gitops#181 and
push it:

```bash
helm package charts/rhai-on-openshift-chart --version 3.6.0-aigrid.dev-<grid sha>
helm push <chart>.tgz oci://quay.io:443/sbatsche --registry-config ~/.config/containers/auth.json
```

## Front door

`frontDoor.mode` picks where clients enter. The demo runs maas.

- grid: clients call grid.acme.lab, and hq's grid gateway checks each API key, then routes.
- maas: clients call maas.acme.lab. MaaS checks the key, applies the token limit and meters
  the request, then forwards to hq's grid gateway, which only routes.

In maas mode, POST to https://maas.acme.lab/v1/chat/completions with model
qwen3-coder-30b-a3b or qwen2-5-7b-instruct and the key in ~/.config/grid-acme-maas-key.
client/front-door.yaml lists the current URLs. To switch, change the mode, render, commit;
Argo CD prunes the other mode's objects. In maas mode the grid gateway runs without its own
authentication, behind a NetworkPolicy that admits only the MaaS gateway: lab only.

## Observability

observe.acme.lab serves the demo Grafana (anonymous Viewer, lab only). It reads hq's Thanos
Querier and LokiStack. Factory and retail appear through hq's operator, which polls them.
helpers/grid-errors.sh --follow prints new errors and panics from every site.

## Findings

- A k3s import pulls nothing until the MultiClusterHub has `imagePullSecret`; set it first.
- GitOpsCluster refuses a k3s cluster with no API URL; set `managedClusterClientConfigs`.
- swim.<hub>.acme.lab is every site's gossip seed, so its pinned address must not move.
- Invite tokens never enter Git: each site's Policy reads its own token on the hub at apply
  time.
- Argo CD prunes and deletes despite Helm's keep annotation; use `Prune=false,Delete=false`.
- A CRD and the resources that use its new field cannot share an app. Argo diffs against
  the live CRD, fails with "field not declared in schema", and syncs nothing, the CRD
  included. Each site's CRDs sync in grid-SITE-crds; the site app retries until they land.
- setup/argocd.yaml binds Argo CD's controller to cluster-admin: lab only.
