#!/usr/bin/env python3
"""Sample each site's vLLM and EPP metrics once a second, for per-site attribution.

One long-lived kubectl exec per endpoint loops inside the pod and streams filtered
Prometheus lines, so a 1s cadence costs no exec setup per sample. Writes JSON lines:
{"t": epoch, "site": name, "src": "vllm"|"epp", "m": {series: value}}.

  sites_sampler.py OUT.jsonl [--seconds N] [--epp-token-cmd CMD]

EPP endpoints on KServe need a bearer with get on /metrics. EPP_TOKEN_<SITE> env vars
(hq_east, hq_west, retail) supply it on stdin, never on a command line; a site without a
token samples vLLM only.
"""

import argparse
import json
import os
import subprocess
import sys
import threading
import time

GKC = os.environ.get("GRID_KUBECONFIG", os.path.expanduser("~/.kube/grid.kubeconfig"))
RKC = os.environ.get("RETAIL_KUBECONFIG", os.path.expanduser("~/.local/share/sno/site-e-dagobah/install/auth/kubeconfig"))

VLLM_KEEP = ("vllm:num_requests_running", "vllm:num_requests_waiting", "vllm:request_success_total",
             "vllm:prefix_cache_hits_total", "vllm:prefix_cache_queries_total", "vllm:kv_cache_usage_perc",
             "vllm:time_to_first_token_seconds_bucket", "vllm:time_to_first_token_seconds_count",
             "vllm:time_to_first_token_seconds_sum", "vllm:inter_token_latency_seconds_sum",
             "vllm:inter_token_latency_seconds_count", "vllm:prompt_tokens_total", "vllm:generation_tokens_total")
EPP_KEEP = ("llm_d_epp_request_ttft_seconds_bucket", "llm_d_epp_request_ttft_seconds_sum",
            "llm_d_epp_request_ttft_seconds_count", "llm_d_epp_request_tpot_seconds_sum",
            "llm_d_epp_request_tpot_seconds_count", "llm_d_epp_request_input_tokens_sum",
            "llm_d_epp_request_input_tokens_count", "llm_d_epp_request_cached_tokens_sum",
            "llm_d_epp_request_cached_tokens_count", "llm_d_epp_request_error_total", "llm_d_epp_request_total")

# site: (kubeconfig, context, namespace, vllm deployment, vllm container, vllm url, epp exec target, epp url)
SITES = {
    "hq-east": (GKC, "dagobah", "ai-tenant-site-a", "qwen3-kserve", "main", "https://localhost:8000/metrics",
                "deploy/qwen3-kserve-router-scheduler", "https://localhost:9090/metrics"),
    "hq-west": (GKC, "dagobah", "ai-tenant-site-b", "qwen3-kserve", "main", "https://localhost:8000/metrics",
                "deploy/qwen3-kserve-router-scheduler", "https://localhost:9090/metrics"),
    "factory": (GKC, "site-d", "llm-d", "optimized-baseline-nvidia-gpu-vllm-decode", "modelserver",
                "http://localhost:8000/metrics", None, "http://optimized-baseline-epp.llm-d.svc:9090/metrics"),
    "retail": (RKC, "admin", "ai-tenant-retail", "qwen-retail-kserve", "main", "https://localhost:8000/metrics",
               "deploy/qwen-retail-kserve-router-scheduler", "https://localhost:9090/metrics"),
}

LOOP = ('read -r T; while :; do echo "@@ $(date +%s.%N)"; '
        'curl -sk -m 2 ${T:+-H "Authorization: Bearer $T"} "$U" | grep -E "^(KEEP)"; sleep 1; done')

lock = threading.Lock()


def parse_stream(proc, site, src, out, stop):
    sample, t = {}, None
    for line in proc.stdout:
        if stop.is_set():
            break
        line = line.rstrip("\n")
        if line.startswith("@@ "):
            if t is not None and sample:
                with lock:
                    out.write(json.dumps({"t": t, "site": site, "src": src, "m": sample}) + "\n")
                    out.flush()
            t, sample = float(line[3:]), {}
            continue
        name, _, value = line.rpartition(" ")
        try:
            sample[name] = float(value)
        except ValueError:
            pass


def start(site, src, out, stop):
    kc, ctx, ns, deploy, container, vurl, epp_target, eurl = SITES[site]
    keep = "|".join(k.replace(":", ":") for k in (VLLM_KEEP if src == "vllm" else EPP_KEEP))
    if src == "vllm":
        target, cflag, url, token = f"deploy/{deploy}", ["-c", container], vurl, ""
    else:
        token = os.environ.get("EPP_TOKEN_" + site.replace("-", "_"), "")
        if epp_target is None:  # factory: reach the EPP Service from the vLLM pod
            target, cflag, url = f"deploy/{deploy}", ["-c", container], eurl
        elif not token:
            return None
        else:
            target, cflag, url = epp_target, [], eurl
    script = LOOP.replace("KEEP", keep)
    proc = subprocess.Popen(["kubectl", "--kubeconfig", kc, "--context", ctx, "-n", ns, "exec", "-i", target, *cflag,
                             "--", "env", f"U={url}", "sh", "-c", script],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    proc.stdin.write(token + "\n")
    proc.stdin.flush()
    th = threading.Thread(target=parse_stream, args=(proc, site, src, out, stop), daemon=True)
    th.start()
    return proc


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("out")
    p.add_argument("--seconds", type=int, default=0, help="stop after N seconds; 0 runs until killed")
    p.add_argument("--sites", default=",".join(SITES))
    a = p.parse_args()
    stop = threading.Event()
    with open(a.out, "a") as out:
        procs = [pr for s in a.sites.split(",") for src in ("vllm", "epp") if (pr := start(s, src, out, stop))]
        print(f"sampling {len(procs)} endpoints into {a.out}", file=sys.stderr, flush=True)
        try:
            end = time.time() + a.seconds if a.seconds else None
            while end is None or time.time() < end:
                time.sleep(1)
        except KeyboardInterrupt:
            pass
        finally:
            stop.set()
            for pr in procs:
                pr.terminate()


if __name__ == "__main__":
    main()
