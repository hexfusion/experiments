#!/usr/bin/env python3
"""Every 5s while a benchmark runs: each site's vLLM queue and KV cache, the front door's
per-cluster split, health demotions, provider readiness, GPU-node memory, and the key
directory (maas-api CPU and the gateway's key-lookup timeouts). Deletes the run's Jobs and
exits 3 on a guardrail: a vLLM or maas-api restart, a provider Ready=False, or a node under
the memory floor. Auth timeouts are recorded, not a guardrail: the run measures where they start.
Prints one CSV row per tick until the run's Jobs finish.

  watch.py --run RUN [--floor-gib 2]
"""

import argparse
import datetime
import json
import os
import re
import subprocess
import sys
import time

# site: (kubeconfig or None for --kubeconfig, context, namespace, deployment, container, metrics URL)
RETAIL_KC = os.path.expanduser("~/.local/share/sno/site-e-dagobah/install/auth/kubeconfig")
SITES = {
    "hq-east": (None, "dagobah", "ai-tenant-site-a", "qwen3-kserve", "main", "https://localhost:8000/metrics"),
    "hq-west": (None, "dagobah", "ai-tenant-site-b", "qwen3-kserve", "main", "https://localhost:8000/metrics"),
    "factory": (None, "site-d", "llm-d", "optimized-baseline-nvidia-gpu-vllm-decode", "modelserver", "http://localhost:8000/metrics"),
    "retail": (RETAIL_KC, "admin", "ai-tenant-retail", "qwen-retail-kserve", "main", "https://localhost:8000/metrics"),
}
NODES = ["endor.dagobah.hexfusion.local", "worker2.dagobah.hexfusion.local"]
DEMOTION = "no healthy endpoint changed"
AUTH_TIMEOUT = "directory could not answer"
# The key directory the gateway's api-key plugin calls per request.
MAAS = ("redhat-ai-gateway-infra", "maas-api")


def kubectl(a, context, *args, kubeconfig=None):
    out = subprocess.run(["kubectl", "--kubeconfig", kubeconfig or a.kubeconfig, "--context", context, *args],
                         capture_output=True, text=True, timeout=30)
    return out.stdout


def vllm(a, site):
    kc, context, ns, deploy, container, url = SITES[site]
    text = kubectl(a, context, "-n", ns, "exec", f"deployment/{deploy}", "-c", container, "--",
                   "curl", "-sk", "-m", "3", url, kubeconfig=kc)
    def get(*names):
        for name in names:
            m = re.search(rf"^vllm:{name}\{{[^}}]*\}} (\S+)", text, re.M)
            if m:
                return float(m.group(1))
        return None
    return get("num_requests_running"), get("num_requests_waiting"), get("kv_cache_usage_perc", "gpu_cache_usage_perc")


def restarts(a, site):
    kc, context, ns, deploy, _, _ = SITES[site]
    pods = json.loads(kubectl(a, context, "-n", ns, "get", "pods", "-o", "json", kubeconfig=kc) or '{"items":[]}')["items"]
    return {p["metadata"]["name"]: sum(c.get("restartCount", 0) for c in p["status"].get("containerStatuses", []))
            for p in pods if p["metadata"]["name"].startswith(deploy + "-") and "router" not in p["metadata"]["name"]}


def maas(a):
    """maas-api pod restarts by name, and its CPU in millicores from the metrics API."""
    ns, deploy = MAAS
    pods = json.loads(kubectl(a, "dagobah", "-n", ns, "get", "pods", "-o", "json") or '{"items":[]}')["items"]
    mine = [p for p in pods if p["metadata"]["name"].startswith(deploy + "-")
            and p["metadata"]["name"][len(deploy) + 1:].count("-") == 1]
    restarts = {p["metadata"]["name"]: sum(c.get("restartCount", 0) for c in p["status"].get("containerStatuses", []))
                for p in mine}
    cpu = None
    for pod in restarts:
        raw = kubectl(a, "dagobah", "get", "--raw", f"/apis/metrics.k8s.io/v1beta1/namespaces/{ns}/pods/{pod}")
        try:
            usage = json.loads(raw)["containers"][0]["usage"]["cpu"]
            cpu = int(usage[:-1]) / 1e6 if usage.endswith("n") else float(usage.rstrip("m"))
        except (ValueError, KeyError, IndexError, TypeError):
            pass
    return restarts, cpu


def validations(a):
    """maas-api's key-validate calls so far: count and total seconds, from its metrics port."""
    ns, deploy = MAAS
    text = kubectl(a, "dagobah", "-n", ns, "exec", f"deployment/{deploy}", "--", "curl", "-s", "-m", "3",
                   "http://localhost:9090/metrics")
    count = total = 0.0
    for kind, value in re.findall(r'^maas_api_http_request_duration_seconds_(count|sum)\{[^}]*route="/internal/v1/api-keys/validate"[^}]*\}\s+(\S+)',
                                  text, re.M):
        if kind == "count":
            count += float(value)
        else:
            total += float(value)
    return count, total


def split(a):
    text = kubectl(a, "dagobah", "-n", "grid", "exec", "deployment/grid-gateway", "-c", "praxis", "--", "wget", "-qO-",
                   "-T", "3", "--header", "Host: 127.0.0.1", "http://127.0.0.1:9901/metrics")
    counts = {}
    for m in re.finditer(r'^praxis_upstream_requests_total\{([^}]*)\}\s+(\S+)', text, re.M):
        cluster = dict(re.findall(r'(\w+)="([^"]*)"', m.group(1))).get("cluster", "?")
        counts[cluster] = counts.get(cluster, 0) + float(m.group(2))
    return counts


def not_ready(a):
    items = json.loads(kubectl(a, "dagobah", "get", "inferenceproviders", "-A", "-o", "json") or '{"items":[]}')["items"]
    return [i["metadata"]["name"] for i in items
            if any(c.get("type") == "Ready" and c.get("status") == "False" for c in i.get("status", {}).get("conditions", []))]


def available_gib(a, node):
    raw = kubectl(a, "dagobah", "get", "--raw", f"/api/v1/nodes/{node}/proxy/stats/summary")
    try:
        return json.loads(raw)["node"]["memory"]["availableBytes"] / 2**30
    except (ValueError, KeyError, TypeError):
        return None


def gateway_log(a, since):
    """Health demotions and key-lookup timeouts in the gateway log since `since`."""
    logs = kubectl(a, "dagobah", "-n", "grid", "logs", "deployment/grid-gateway", "-c", "praxis", f"--since-time={since}")
    lines = logs.splitlines()
    return sum(DEMOTION in line for line in lines), sum(AUTH_TIMEOUT in line for line in lines)


def jobs_done(a):
    jobs = json.loads(kubectl(a, "dagobah", "-n", "grid-load", "get", "jobs", "-l", f"run={a.run}", "-o", "json")
                      or '{"items":[]}')["items"]
    # A Job is done once its guidellm printed the done marker; the pod then idles for the copy.
    return bool(jobs) and all(
        "GRIDBENCH DONE" in kubectl(a, "dagobah", "-n", "grid-load", "logs", f"job/{j['metadata']['name']}", "--tail=5")
        for j in jobs)


def abort(a, why):
    print(f"GUARDRAIL {why}; deleting run {a.run}", file=sys.stderr, flush=True)
    kubectl(a, "dagobah", "-n", "grid-load", "delete", "jobs", "-l", f"run={a.run}", "--wait=false")
    sys.exit(3)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--run", required=True)
    p.add_argument("--kubeconfig", default=os.path.expanduser("~/.kube/grid.kubeconfig"))
    p.add_argument("--floor-gib", type=float, default=2.0)
    p.add_argument("--record-not-ready", action="store_true",
                   help="log providers with Ready=False instead of stopping the run (when readiness is under test)")
    a = p.parse_args()
    since = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    baseline = {site: restarts(a, site) for site in SITES}
    maas_baseline, _ = maas(a)
    last_validate = validations(a)
    last = split(a)
    print("time," + ",".join(f"{s}_running,{s}_waiting,{s}_kv" for s in SITES) + ",split,demotions,auth_timeouts,"
          + "maas_cpu_m,validates,validate_ms,not_ready," + ",".join(f"{n.split('.')[0]}_avail_gib" for n in NODES), flush=True)
    while True:
        tick = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        row = [time.strftime("%H:%M:%S")]
        for site in SITES:
            row += ["" if v is None else f"{v:g}" for v in vllm(a, site)]
            now = restarts(a, site)
            if now != baseline[site] and (set(now) != set(baseline[site]) or any(now[k] > baseline[site][k] for k in now)):
                abort(a, f"{site} vLLM restarted or replaced: {baseline[site]} -> {now}")
        counts = split(a)
        row.append(" ".join(f"{c}:{counts[c] - last.get(c, 0):g}" for c in sorted(counts) if counts[c] > last.get(c, 0)))
        last = counts
        demoted, timeouts = gateway_log(a, since)
        since = tick
        row += [str(demoted), str(timeouts)]
        maas_now, cpu = maas(a)
        row.append("" if cpu is None else f"{cpu:.0f}")
        now_validate = validations(a)
        calls = now_validate[0] - last_validate[0]
        row += [f"{calls:g}", f"{1000 * (now_validate[1] - last_validate[1]) / calls:.1f}" if calls > 0 else ""]
        last_validate = now_validate
        if maas_now != maas_baseline:
            abort(a, f"maas-api restarted or replaced: {maas_baseline} -> {maas_now}")
        bad = not_ready(a)
        row.append(" ".join(bad))
        if bad and not a.record_not_ready:
            abort(a, f"providers Ready=False: {bad}")
        for node in NODES:
            gib = available_gib(a, node)
            row.append("" if gib is None else f"{gib:.1f}")
            if gib is not None and gib < a.floor_gib:
                abort(a, f"{node} has {gib:.1f} GiB available")
        print(",".join(row), flush=True)
        if jobs_done(a):
            return
        time.sleep(5)


if __name__ == "__main__":
    main()
