# Sites

| Site | Role | Configuration | Workload owner |
|---|---|---|---|
| Dagobah | OpenShift hub and local provider | [site.yaml](dagobah/site.yaml), [gateway.yaml](dagobah/gateway.yaml) | Argo for local models and observability; Argo auto-syncs the hub gateway |
| site-d | k3s remote provider, imported into ACM | [site.yaml](site-d/site.yaml), [gateway.yaml](site-d/gateway.yaml) | Existing Helm gateway and model are manual; ACM manages its agents |

`site.yaml` describes cluster identity and placement. `gateway.yaml` describes that
site's gateway. Model definitions live in `../models/` and are referenced by the site.
Dagobah's `maas.yaml` records its existing manual MaaS integration inputs; no Application
currently applies that file. Files in a site directory are not recursively applied.

To add a site, define its role, import it into ACM, configure its model/runtime and
monitoring, then enroll its Grid identity and explicitly attach it to the intended
GridNetwork. Enrollment credentials stay in Secrets outside Git. Record ownership in
`topology.yaml` so readers can distinguish Argo-managed resources from existing workloads.

ACM membership, Grid enrollment, model readiness and successful routed inference are
separate acceptance checks. Inspect real pods and requests in addition to CR conditions.
