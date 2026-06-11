#!/usr/bin/env python3
# Synthesize a run's raw artifacts in results/ into the per-arm conclusion and
# the capacity-formula table (see README/MODEL.md). Reads k6 summary JSON per arm
# + the latest envoy-stats / pod-resources CSV. Tolerant of missing files.
#
# Usage: scripts/summarize.py [results_dir]   (default ./results)

import csv
import glob
import json
import os
import sys

RES = sys.argv[1] if len(sys.argv) > 1 else "results"


def latest(pat):
    fs = sorted(glob.glob(os.path.join(RES, pat)))
    return fs[-1] if fs else None


def num(x):
    try:
        return float(x)
    except (TypeError, ValueError):
        return None


def envoy_summary(path):
    if not path:
        return None
    onset = None
    peak_active = 0.0
    rows = list(csv.DictReader(open(path)))
    for r in rows:
        active = num(r.get("rq_active")) or 0.0
        peak_active = max(peak_active, active)
        po = num(r.get("rq_pending_overflow")) or 0.0
        if onset is None and po > 0:
            onset = {"rq_active": active, "rq_open": r.get("rq_open"),
                     "cx_active": r.get("cx_active")}
    total_overflow = (num(rows[-1].get("rq_pending_overflow")) or 0) - \
                     (num(rows[0].get("rq_pending_overflow")) or 0) if rows else 0
    return {"onset": onset, "peak_active": peak_active, "total_overflow": total_overflow}


def res_summary(path):
    if not path:
        return None
    peak_cpu_pct = peak_fds = peak_conns = 0.0
    for r in csv.DictReader(open(path)):
        cpu, lim = num(r.get("gw_cpu_m")), num(r.get("gw_cpu_limit_m"))
        if cpu and lim:
            peak_cpu_pct = max(peak_cpu_pct, 100 * cpu / lim)
        peak_fds = max(peak_fds, num(r.get("gw_fds")) or 0)
        peak_conns = max(peak_conns, num(r.get("envoy_total_conns")) or 0)
    return {"peak_cpu_pct": peak_cpu_pct, "peak_fds": peak_fds, "peak_conns": peak_conns}


def k6_summary(path):
    m = json.load(open(path)).get("metrics", {})
    def g(k, f):
        return (m.get(k, {}).get("values", {}) or {}).get(f)
    return {
        "reqs": g("reqs_total", "count"),
        "n500": g("reqs_500", "count"),
        "overflow500": g("reqs_overflow_500", "count"),
        "success": g("req_success", "rate"),
        "ttft_p95": g("ttft_ms", "p(95)"),
        "vus_p95": g("vus_active", "p(95)"),
    }


ev = envoy_summary(latest("envoy-stats-*.csv"))
rs = res_summary(latest("pod-resources-*.csv"))

print(f"# Bench summary ({RES})\n")
for f in sorted(glob.glob(os.path.join(RES, "k6-summary*.json"))):
    arm = os.path.basename(f).replace("k6-summary-", "").replace(".json", "")
    k = k6_summary(f)
    sr = f"{k['success']*100:.1f}%" if k["success"] is not None else "?"
    print(f"## arm {arm}")
    print(f"- reqs={k['reqs']} 500={k['n500']} overflow500={k['overflow500']} "
          f"success={sr} ttft_p95={k['ttft_p95'] and round(k['ttft_p95'])}ms "
          f"concurrency_p95≈{k['vus_p95'] and round(k['vus_p95'])}")

if ev:
    o = ev["onset"]
    print("\n## envoy ext_proc cluster")
    if o:
        print(f"- overflow onset at rq_active≈{round(o['rq_active'])} "
              f"(rq_open={o['rq_open']}) → measured C_extproc")
    else:
        print("- no overflow observed")
    print(f"- peak rq_active={round(ev['peak_active'])} total_overflow={round(ev['total_overflow'])}")

if rs:
    print("\n## gateway pod (A2 binding resource)")
    print(f"- peak CPU={rs['peak_cpu_pct']:.0f}% of limit  peak FDs={round(rs['peak_fds'])}  "
          f"peak conns={round(rs['peak_conns'])}")
    if ev and ev["onset"]:
        verdict = "breaker-bound (A1)" if rs["peak_cpu_pct"] < 85 else "CPU-bound (A2)"
        print(f"- overflow with CPU {rs['peak_cpu_pct']:.0f}% of limit → {verdict}")

print("\n_Fill findings.md from the above; the EPP inflight gauge "
      "(llm_d_router_epp_extproc_streams_inflight) cross-checks rq_active via Prometheus._")
