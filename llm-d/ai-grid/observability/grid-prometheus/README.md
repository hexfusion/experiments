# Standalone grid Prometheus — decoupled from OCP monitoring

Dedicated Prometheus for the grid, so the dashboard does NOT depend on OpenShift's
in-cluster (thanos/user-workload) monitoring. Scrapes all sites directly, including the
EXTERNAL site-d VM which OCP can't scrape.

- **grid-prometheus.yaml** — Prometheus (grid-system) scraping:
  - site-a: `qwen3-kserve-workload-svc.ai-tenant-site-a.svc:8000/metrics` (https, skip-verify), labelled namespace=ai-tenant-site-a, site=site-a
  - site-b: same for site-b
  - site-d: `192.168.1.150:30080/metrics` (http, the external GPU VM), namespace=ai-tenant-site-d, site=site-d
  - token-meter: `token-meter.grid-system.svc:9110/metrics` (grid_consumer_tokens_* for the token-balance panel)
- **grafana-datasource-thanos.yaml** — the grafana datasource (uid `thanos`, kept so the
  ai-grid dashboard resolves) repointed from OCP thanos-querier to
  `http://grid-prometheus.grid-system.svc:9090`. No OCP token/URL.

Result: the ai-grid dashboard's vLLM Load/Latency/Throughput panels + token balance are
served by the grid's own Prometheus and show site-a/b/d (site-d now included). The routing
panels still use Tempo (traces). Applied live on dagobah. The dashboard JSON already carries
site-d color/name overrides (../grafana/dashboards/ai-grid.json).
