#!/usr/bin/env python3
"""Summarize a burst run: per phase (in a burst, the 10s after, steady) the request count, status
codes, first-token and end-to-end quantiles, then per-site completions in WINDOW-second windows.

  burst_report.py RUN_DIR [--period 30] [--burst-secs 3] [--window 1]
"""

import argparse
import collections
import csv
import json
import os


def quantiles(values, qs=(0.5, 0.9, 0.99)):
    values = sorted(values)
    return [values[min(len(values) - 1, int(q * len(values)))] if values else None for q in qs]


def fmt(v):
    return "-" if v is None else f"{v:.2f}"


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("run_dir")
    p.add_argument("--period", type=float, default=30)
    p.add_argument("--burst-secs", type=float, default=3)
    p.add_argument("--window", type=float, default=1)
    a = p.parse_args()
    lines = open(os.path.join(a.run_dir, "requests.csv")).read().splitlines()
    t0 = float(next(l for l in lines if l.startswith("# t0")).split()[2])
    rows = list(csv.DictReader(l for l in lines if not l.startswith("#")))

    def phase(start):
        m = (start - t0) % a.period
        return "burst" if m < a.burst_secs else ("after" if m < a.burst_secs + 10 else "steady")

    by = collections.defaultdict(list)
    for r in rows:
        by[phase(float(r["start"]))].append(r)
    print("| Phase | Requests | Codes | TTFT p50/p90/p99 s | e2e p50/p90/p99 s |")
    print("|---|---|---|---|---|")
    for name in ("burst", "after", "steady"):
        v = by[name]
        ok = [r for r in v if r["code"] == "200"]
        codes = collections.Counter(r["code"] for r in v)
        ttft = quantiles([float(r["ttft_s"]) for r in ok if r["ttft_s"]])
        e2e = quantiles([float(r["total_s"]) for r in ok])
        print(f"| {name} | {len(v)} | {' '.join(f'{k}:{n}' for k, n in sorted(codes.items()))} "
              f"| {'/'.join(fmt(x) for x in ttft)} | {'/'.join(fmt(x) for x in e2e)} |")

    series = collections.defaultdict(list)
    for line in open(os.path.join(a.run_dir, "sites.jsonl")):
        r = json.loads(line)
        if r["src"] == "vllm":
            done = sum(v for k, v in r["m"].items() if k.startswith("vllm:request_success_total"))
            series[r["site"]].append((r["t"], done))
    for v in series.values():
        v.sort()
    sites = sorted(series)

    def at(site, t):
        best = None
        for ts, done in series[site]:
            if ts > t:
                break
            best = done
        return best

    end = max(float(r["start"]) + float(r["total_s"]) for r in rows)
    print(f"\n| t+s | phase | " + " | ".join(sites) + " |")
    print("|---|---|" + "---|" * len(sites))
    t = t0
    while t < end:
        d = {s: (at(s, t + a.window) or 0) - (at(s, t) or 0) for s in sites}
        if any(d.values()):
            print(f"| {t - t0:.0f} | {phase(t)} | " + " | ".join(f"{d[s]:.0f}" for s in sites) + " |")
        t += a.window


if __name__ == "__main__":
    main()
