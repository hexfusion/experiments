#!/usr/bin/env python3
"""Readiness soak: once a minute, with no load, record every InferenceProvider's Ready status
and reason, each site gateway's health-demotion lines, front-door 5xx by cluster, and
serving-config reload results. Writes a CSV row per sample and an event line per transition.

  soak.py --out DIR [--interval 60] [--hours 48]

Read-only. A 5xx with no upstream cluster is the router's own answer, where the 503s come from.
"""

import argparse
import datetime
import json
import os
import re
import subprocess
import time

CONTEXTS = ["dagobah", "site-d"]
DEMOTION = "no healthy endpoint changed"


def kubectl(a, context, *args):
    try:
        out = subprocess.run(["kubectl", "--kubeconfig", a.kubeconfig, "--context", context, *args],
                             capture_output=True, text=True, timeout=45)
    except subprocess.TimeoutExpired:
        return None
    return out.stdout if out.returncode == 0 else None


def providers(a, context):
    raw = kubectl(a, context, "get", "inferenceproviders", "-A", "-o", "json")
    if raw is None:
        return None
    state = {}
    for item in json.loads(raw)["items"]:
        ready = next((c for c in item.get("status", {}).get("conditions", []) if c.get("type") == "Ready"), {})
        state[f"{context}/{item['metadata']['name']}"] = (ready.get("status", "?"), ready.get("reason", ""))
    return state


def gateway_metrics(a, context):
    text = kubectl(a, context, "-n", "grid", "exec", "deployment/grid-gateway", "-c", "praxis", "--", "wget", "-qO-",
                   "-T", "5", "--header", "Host: 127.0.0.1", "http://127.0.0.1:9901/metrics")
    if text is None:
        return None
    fivexx, reloads = {}, {}
    for m in re.finditer(r'^praxis_http_requests_total\{([^}]*)\}\s+(\S+)', text, re.M):
        labels = dict(re.findall(r'(\w+)="([^"]*)"', m.group(1)))
        if labels.get("status_class") == "5xx":
            cluster = labels.get("cluster") or "router"
            fivexx[cluster] = fivexx.get(cluster, 0) + float(m.group(2))
    for m in re.finditer(r'^grid_serving_config_reload_total\{result="([^"]*)"\}\s+(\S+)', text, re.M):
        reloads[m.group(1)] = float(m.group(2))
    return fivexx, reloads


def demotions(a, context, since):
    logs = kubectl(a, context, "-n", "grid", "logs", "deployment/grid-gateway", "-c", "praxis", f"--since-time={since}")
    return None if logs is None else [line for line in logs.splitlines() if DEMOTION in line]


def now_utc():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--out", required=True)
    p.add_argument("--kubeconfig", default=os.path.expanduser("~/.kube/grid.kubeconfig"))
    p.add_argument("--interval", type=int, default=60)
    p.add_argument("--hours", type=float, default=48)
    a = p.parse_args()
    os.makedirs(a.out, exist_ok=True)
    csv = open(os.path.join(a.out, "soak.csv"), "a", buffering=1)
    events = open(os.path.join(a.out, "events.log"), "a", buffering=1)
    if csv.tell() == 0:
        csv.write("time,not_ready,demotions,fivexx,reloads,unreachable\n")
    last_state, last_metrics, since = {}, {}, now_utc()
    end = time.time() + a.hours * 3600
    while time.time() < end:
        stamp = now_utc()
        state, unreachable, demoted, fivexx, reloads = {}, [], [], {}, {}
        for context in CONTEXTS:
            seen = providers(a, context)
            if seen is None:
                unreachable.append(f"{context}:providers")
            else:
                state.update(seen)
            metrics = gateway_metrics(a, context)
            if metrics is None:
                unreachable.append(f"{context}:gateway")
            else:
                for cluster, count in metrics[0].items():
                    fivexx[f"{context}/{cluster}"] = count
                for result, count in metrics[1].items():
                    reloads[f"{context}/{result}"] = count
            lines = demotions(a, context, since)
            if lines is None:
                unreachable.append(f"{context}:logs")
            else:
                demoted += [f"{context}: {line[-160:]}" for line in lines]
        since = stamp
        for name, value in sorted(state.items()):
            if last_state.get(name) != value:
                events.write(f"{stamp} provider {name} Ready={value[0]} reason={value[1]} (was {last_state.get(name)})\n")
        for line in demoted:
            events.write(f"{stamp} demotion {line}\n")
        for key, count in sorted({**fivexx, **reloads}.items()):
            if last_metrics.get(key) is not None and count < last_metrics[key]:
                events.write(f"{stamp} counter reset {key} (gateway restarted?)\n")
            elif last_metrics.get(key, 0) != count and key in fivexx:
                events.write(f"{stamp} 5xx {key} +{count - last_metrics.get(key, 0):g}\n")
            elif last_metrics.get(key, 0) != count:
                events.write(f"{stamp} reload {key} +{count - last_metrics.get(key, 0):g}\n")
        last_state.update(state)
        last_metrics.update({**fivexx, **reloads})
        not_ready = " ".join(n for n, v in sorted(state.items()) if v[0] != "True")
        csv.write(f"{stamp},{not_ready},{len(demoted)},"
                  f"{' '.join(f'{k}:{v:g}' for k, v in sorted(fivexx.items()))},"
                  f"{' '.join(f'{k}:{v:g}' for k, v in sorted(reloads.items()))},{' '.join(unreachable)}\n")
        time.sleep(a.interval)


if __name__ == "__main__":
    main()
