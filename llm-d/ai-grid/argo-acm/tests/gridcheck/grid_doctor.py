#!/usr/bin/env python3
"""Walk one site's signals-based routing chain and name the first broken link.

Links, in the order a request depends on them:
  1 providers  every InferenceProvider declares metricsConfig and its signals are fresh
  2 mode       poll signals, served by the operator, with scoringPolicy left at noMetrics
  3 signals    every peer the gateway polls answers over mTLS with fresh samples
  4 operator   the serving ConfigMap lists candidates and peers for the gateway
  5 gateway    praxis routes with grid_site_route, mounts the serving config, and applied it
  6 probe      (--probe N) front-door requests succeed and spread across the candidates

Prints one PASS/FAIL/SKIP line per link and exits nonzero when any link fails.
Reads the site's identity into a 0700 temp dir that is removed on exit.
"""

import argparse
import base64
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import urllib.request
import ssl

import mtls

SITE_TTL_S = 30


class Doctor:
    def __init__(self, context, kubeconfig, namespace):
        self.context, self.kubeconfig, self.ns = context, kubeconfig, namespace
        self.rows = []

    def kubectl(self, *args, namespaced=True):
        cmd = ["kubectl", "--kubeconfig", self.kubeconfig, "--context", self.context]
        if namespaced:
            cmd += ["-n", self.ns]
        out = subprocess.run(cmd + list(args), capture_output=True, text=True, timeout=60)
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:200])
        return out.stdout

    def json(self, *args, namespaced=True):
        return json.loads(self.kubectl(*args, "-o", "json", namespaced=namespaced))

    def report(self, link, verdict, reason):
        self.rows.append((link, verdict, reason))
        print(f"{verdict:4}  {link:<10} {reason}", flush=True)

    def secret_to(self, name, key, path):
        data = self.json("get", "secret", name)["data"][key]
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(base64.b64decode(data))

    def lb_ip(self, service):
        svc = self.json("get", "svc", service)
        return svc["status"]["loadBalancer"]["ingress"][0]["ip"]


def fresh_samples(result, site):
    """Samples from `result` within the site TTL, and the response's Date."""
    samples, _ = mtls.parse_exposition(result.body)
    date = mtls.date_epoch(result.headers) or time.time()
    return [s for s in samples if s.timestamp_ms and date - s.timestamp_ms / 1000 <= SITE_TTL_S + 1], samples


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--context", required=True, help="the site's kube context")
    p.add_argument("--kubeconfig", default=os.path.expanduser("~/.kube/grid.kubeconfig"))
    p.add_argument("--namespace", default="grid")
    p.add_argument("--gateway", default="grid-gateway", help="the gateway Deployment")
    p.add_argument("--probe", type=int, default=0, help="send N front-door requests and report the spread")
    p.add_argument("--front-door", default="https://grid.apps.dagobah.hexfusion.local/v1/chat/completions")
    p.add_argument("--resolve", default="192.168.1.11", help="IP the front-door host resolves to")
    p.add_argument("--key-file", default=os.path.expanduser("~/.config/grid-dagobah-maas-key"))
    p.add_argument("--model", default="Qwen3-Coder-30B-A3B")
    a = p.parse_args()
    d = Doctor(a.context, a.kubeconfig, a.namespace)

    network = d.json("get", "gridnetwork", namespaced=False)["items"][0]
    serving_cm = f"grid-serving-{network['metadata']['name']}-{a.gateway}"
    try:
        serving = json.loads(d.json("get", "configmap", serving_cm)["data"]["serving-config.json"])
    except (RuntimeError, KeyError) as err:
        serving = None
        serving_err = str(err)

    # 1 providers
    providers = d.json("get", "inferenceproviders", namespaced=False)["items"]
    bare = [pr["metadata"]["name"] for pr in providers if not pr["spec"].get("metricsConfig")]
    with tempfile.TemporaryDirectory(prefix="grid-doctor-") as tmp:
        os.chmod(tmp, 0o700)
        crt, key, ca = (os.path.join(tmp, n) for n in ("tls.crt", "tls.key", "ca.crt"))
        d.secret_to("grid-site-identity", "tls.crt", crt)
        d.secret_to("grid-site-identity", "tls.key", key)
        d.secret_to(network["spec"]["tls"]["caSecretRef"]["name"], "ca.crt", ca)
        own_ip = d.lb_ip("grid-operator-swim")
        local = mtls.request(own_ip, 9091, ca, crt, key)
        own_fresh, _ = fresh_samples(local, a.context) if local.outcome == "http" else ([], [])
        if not providers:
            d.report("providers", "FAIL", "no InferenceProviders on this site")
        elif bare:
            d.report("providers", "FAIL", f"no metricsConfig on {', '.join(bare)}: they contribute no signals")
        else:
            routing = {pr["spec"].get("routingClusterRef") or pr["metadata"]["name"] for pr in providers}
            have = {s.labels.get("grid_provider") for s in own_fresh if s.labels.get("grid_site") == a.context}
            missing = sorted(routing - have)
            d.report("providers", "FAIL" if missing else "PASS",
                     f"no fresh signals for {missing}" if missing else f"fresh signals for {sorted(routing)}")

        # 2 mode: the grid runs poll signals, which its operator must serve; poll ignores scoringPolicy
        transport = (network["spec"].get("signalTransport") or {}).get("mode", "gossip")
        strategy = (network["spec"].get("scoringPolicy") or {}).get("strategy", "noMetrics")
        operator = d.json("get", "deploy/grid-operator")
        env = {e["name"] for e in operator["spec"]["template"]["spec"]["containers"][0].get("env", [])}
        if transport != "poll":
            d.report("mode", "FAIL", f"signalTransport {transport}: the grid runs with enrollment and poll signals")
        elif "GRID_SIGNALS_LOCAL_ADDR" not in env:
            d.report("mode", "FAIL", "the GridNetwork polls but the operator serves no signals: routing is blind")
        elif strategy != "noMetrics":
            d.report("mode", "FAIL", f"scoringPolicy {strategy} has no effect under poll")
        else:
            d.report("mode", "PASS", "poll, the operator serves signals, the gateway ranks on polled queue depth")

        # 3 signals, every peer the gateway polls, dialed as the gateway does with this site's identity
        if serving is None:
            d.report("signals", "SKIP", f"serving ConfigMap {serving_cm} unreadable: {serving_err}")
        else:
            problems, notes = [], []
            for peer in serving.get("peers", []):
                host, _, port = peer["addr"].rpartition(":")
                if ".svc" in host:
                    host = d.lb_ip(host.split(".")[0])
                r = mtls.request(host, int(port), ca, crt, key, path=peer.get("path", mtls.SIGNALS_PATH))
                if r.outcome != "http" or r.status != 200:
                    problems.append(f"{peer['site']} at {peer['addr']}: {r.outcome} {r.status or r.detail[:60]}")
                    continue
                if r.server_spiffe != mtls.SITE_ID_PREFIX + peer["site"]:
                    problems.append(f"{peer['site']}: server SPIFFE {r.server_spiffe}")
                    continue
                fresh, all_samples = fresh_samples(r, peer["site"])
                if not fresh:
                    problems.append(f"{peer['site']}: reachable, {len(all_samples)} samples, none fresh")
                else:
                    notes.append(f"{peer['site']}: {len(fresh)} fresh samples")
            if not serving.get("peers"):
                d.report("signals", "FAIL", "the serving config lists no peers to poll")
            else:
                d.report("signals", "FAIL" if problems else "PASS", "; ".join(problems or notes))

    # 4 operator
    if serving is None:
        d.report("operator", "FAIL", f"serving ConfigMap {serving_cm} unreadable: {serving_err}")
    else:
        cands = serving.get("candidates", [])
        stale = [c["cluster"] for c in cands if not c.get("fresh")]
        reason = (f"{len(cands)} candidates ({', '.join(c['site'] + '/' + c['cluster'] for c in cands)}), "
                  f"{len(serving.get('peers', []))} peers; scores are not in the ConfigMap, the gateway scores "
                  f"each request from the peers' signals")
        if not cands:
            d.report("operator", "FAIL", "the serving config lists no candidates")
        elif stale:
            d.report("operator", "FAIL", f"candidates not fresh in the overlay: {stale}")
        else:
            d.report("operator", "PASS", reason)

    # 5 gateway
    try:
        dep = d.json("get", "deployment", a.gateway)
        pod_spec = dep["spec"]["template"]["spec"]
        praxis = next(c for c in pod_spec["containers"] if c["name"] == "praxis")
        env = {e["name"]: e.get("value") for e in praxis.get("env", [])}
        cm_name = next(v["configMap"]["name"] for v in pod_spec["volumes"] if v["name"] == "config")
        config = d.json("get", "configmap", cm_name)["data"].get("praxis.yaml", "")
        serving_path = env.get("GRID_SERVING_CONFIG", "")
        mounted = serving_path and any(serving_path.startswith(m["mountPath"]) for m in praxis.get("volumeMounts", []))
        metrics = d.kubectl("exec", f"deploy/{a.gateway}", "-c", "praxis", "--", "wget", "-qO-", "-T", "3",
                            "--header", "Host: 127.0.0.1", "http://127.0.0.1:9901/metrics")
        reloads = {m.group(1): float(m.group(2)) for m in
                   re.finditer(r'^grid_serving_config_reload_total\{[^}]*result="(\w+)"[^}]*\}\s+(\S+)', metrics, re.M)}
        if not serving_path:
            d.report("gateway", "SKIP", "gridServing is off: this gateway serves its own backends and routes no cross-site traffic")
        else:
            faults = []
            if "grid_site_route" not in config:
                faults.append(f"{cm_name} has no grid_site_route filter")
            if not mounted:
                faults.append(f"GRID_SERVING_CONFIG {serving_path!r} is not under a mount")
            # The counters are cumulative, so a past rejection is history, not a fault. The fault is a
            # ConfigMap the gateway cannot parse, which it rejects while serving the last good config.
            if serving is None:
                faults.append(f"{serving_cm} holds a serving config that does not parse; the gateway keeps its last good one")
            in_pod = d.kubectl("exec", f"deploy/{a.gateway}", "-c", "praxis", "--", "cat", serving_path)
            try:
                mounted_differs = serving is not None and json.loads(in_pod) != serving
            except ValueError:
                mounted_differs = True
            if mounted_differs:
                faults.append("the mounted serving config differs from the ConfigMap (kubelet sync pending, or a stale mount)")
            d.report("gateway", "FAIL" if faults else "PASS", "; ".join(faults) or
                     f"grid_site_route in {cm_name}; mounted serving config matches {serving_cm}; reloads since start {reloads or 'none'}")
    except (RuntimeError, StopIteration, KeyError) as err:
        d.report("gateway", "FAIL", f"could not inspect the gateway: {err}")

    # 6 probe
    if a.probe:
        before = upstream_counts(d, a.gateway)
        codes = front_door(a, a.probe)
        after = upstream_counts(d, a.gateway)
        delta = {k: int(after.get(k, 0) - before.get(k, 0)) for k in sorted(set(after) | set(before)) if after.get(k, 0) != before.get(k, 0)}
        ok = codes.count(200) == a.probe and len(delta) > 1
        d.report("probe", "PASS" if ok else "FAIL", f"{codes.count(200)}/{a.probe} returned 200; per-cluster {delta}")
    else:
        d.report("probe", "SKIP", "pass --probe N to send front-door requests")

    first = next((link for link, verdict, _ in d.rows if verdict == "FAIL"), None)
    print(f"\nfirst broken link: {first}" if first else "\nall links pass")
    sys.exit(1 if first else 0)


def upstream_counts(d, gateway):
    text = d.kubectl("exec", f"deploy/{gateway}", "-c", "praxis", "--", "wget", "-qO-", "-T", "3",
                     "--header", "Host: 127.0.0.1", "http://127.0.0.1:9901/metrics")
    counts = {}
    for m in re.finditer(r'^praxis_upstream_requests_total\{([^}]*)\}\s+(\S+)', text, re.M):
        cluster = re.search(r'cluster="([^"]*)"', m.group(1))
        name = cluster.group(1) if cluster else "?"
        counts[name] = counts.get(name, 0) + float(m.group(2))
    return counts


def front_door(a, n):
    """POST n chat completions through the front door; the key never leaves this process."""
    with open(a.key_file) as f:
        key = f.read().strip()
    host = re.match(r"https://([^/:]+)", a.front_door).group(1)
    ctx = ssl.create_default_context()
    ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
    body = json.dumps({"model": a.model, "messages": [{"role": "user", "content": "hi"}], "max_tokens": 8}).encode()
    url = a.front_door.replace(host, a.resolve, 1)
    codes = []
    for _ in range(n):
        req = urllib.request.Request(url, data=body, headers={"Host": host, "Authorization": f"Bearer {key}",
                                                             "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, context=ctx, timeout=60) as resp:
                codes.append(resp.status)
        except urllib.error.HTTPError as err:
            codes.append(err.code)
        except OSError:
            codes.append(0)
    return codes


if __name__ == "__main__":
    main()
