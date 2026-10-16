# Global Grid observability

ACM Thanos is the intended central metrics query backend for Dagobah and site-d.
The global dashboard is organized into overview, load, routing decisions, and cluster
and component health. Gateway, operator and enrollment metrics are required wherever
those components run. Missing or stale telemetry must remain visible.

| Directory | Purpose |
|---|---|
| `acm/` | Existing ACM observability service and metric allowlist |
| `logging/` | Existing shared logging infrastructure |

Both clusters already have ACM collectors. Dagobah uses OpenShift user-workload
monitoring; site-d has ACM-managed Prometheus. Importing a cluster into ACM does not
create ServiceMonitors for Grid components. End-to-end collection and the global
dashboard are being qualified; not every component is deployed at every site.

These are canonical GitOps inputs. The retired `argo-acm` renderer no longer owns them.
