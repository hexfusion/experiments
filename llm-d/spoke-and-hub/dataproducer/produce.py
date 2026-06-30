#!/usr/bin/env python3
# Hub-side DataProducer (POC): consumes a MANUAL list of per-(cluster,pool) spoke EPP /metrics endpoints,
# scrapes each, and produces the (cluster, pool) -> load map. No cross-cluster watch; the hub only knows
# these hand-listed addresses. Proves the hub can consume MULTIPLE EPP/pool metrics from the spokes.
import urllib.request, re

# THE MANUAL LIST (hand-maintained; the hub never discovers these).
ENDPOINTS = [
    ("cluster-a", "model-a", "10.89.0.211:9090"),
    ("cluster-a", "model-b", "10.89.0.213:9090"),
    ("cluster-b", "model-a", "10.89.0.221:9090"),
    ("cluster-b", "model-b", "10.89.0.223:9090"),
    ("cluster-c", "model-a", "10.89.0.231:9090"),
    ("cluster-c", "model-b", "10.89.0.233:9090"),
]
GAUGES = ["inference_pool_average_queue_size",
          "inference_pool_average_running_requests",
          "inference_pool_average_kv_cache_utilization"]

def scrape(addr):
    txt = urllib.request.urlopen("http://%s/metrics" % addr, timeout=5).read().decode()
    out = {}
    for g in GAUGES:
        m = re.search(r'^%s\{[^}]*\}\s+([0-9.eE+-]+)' % re.escape(g), txt, re.M)
        out[g.replace("inference_pool_average_", "")] = float(m.group(1)) if m else None
    return out

print("%-10s %-8s %-14s %7s %8s %6s" % ("cluster", "pool", "endpoint", "queue", "running", "kv"))
for c, p, addr in ENDPOINTS:
    try:
        d = scrape(addr)
        print("%-10s %-8s %-14s %7.2f %8.2f %6.2f" % (c, p, addr, d["queue_size"], d["running_requests"], d["kv_cache_utilization"]))
    except Exception as e:
        print("%-10s %-8s %-14s  scrape ERR: %s" % (c, p, addr, str(e)[:40]))
