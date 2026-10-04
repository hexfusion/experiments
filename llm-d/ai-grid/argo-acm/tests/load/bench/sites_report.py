#!/usr/bin/env python3
"""Per-site attribution for one bench run: guidellm stages joined to sites_sampler.py counters.

For each guidellm stage, per site: requests completed (vLLM request_success delta) and share,
mean and peak running and waiting, rho (mean running / maxRunning), prefix-cache hit ratio,
engine TTFT p50/p90 and mean ITL from vLLM histogram deltas, and, where the EPP sees the
traffic, EPP TTFT p50/p90, mean TPOT, mean input and cached tokens and errors. Then the
per-site share in WINDOW-second windows across the run.

  sites_report.py RUN_DIR [--window 10] [--max-running hq-east=128,hq-west=128,factory=64,retail=64]
"""

import argparse
import collections
import json
import os
import re

LABEL = re.compile(r'(\w+)="([^"]*)"')


def base(series):
    return series.split("{", 1)[0]


def labels(series):
    return dict(LABEL.findall(series))


def load(run_dir):
    samples = collections.defaultdict(list)
    for line in open(os.path.join(run_dir, "sites.jsonl")):
        r = json.loads(line)
        samples[(r["site"], r["src"])].append((r["t"], r["m"]))
    for v in samples.values():
        v.sort(key=lambda x: x[0])
    return samples


def at(series_list, t):
    """The last sample at or before t, else the first after it."""
    best = None
    for ts, m in series_list:
        if ts <= t:
            best = m
        else:
            return best if best is not None else m
    return best


def total(m, name, match=None):
    return sum(v for k, v in (m or {}).items() if base(k) == name and (match is None or match(labels(k))))


def delta(series_list, t0, t1, name, match=None):
    a, b = at(series_list, t0), at(series_list, t1)
    return None if a is None or b is None else total(b, name, match) - total(a, name, match)


def hist_quantiles(series_list, t0, t1, name, qs=(0.5, 0.9), match=None):
    a, b = at(series_list, t0), at(series_list, t1)
    if a is None or b is None:
        return [None] * len(qs)
    buckets = collections.defaultdict(float)
    for k, v in b.items():
        if base(k) == name + "_bucket" and (match is None or match(labels(k))):
            buckets[float(labels(k)["le"].replace("+Inf", "inf"))] += v - a.get(k, 0.0)
    if not buckets or max(buckets.values()) <= 0:
        return [None] * len(qs)
    les = sorted(buckets)
    count = buckets[les[-1]]
    out = []
    for q in qs:
        target, prev_le, prev_c = q * count, 0.0, 0.0
        for le in les:
            c = buckets[le]
            if c >= target:
                if le == float("inf"):
                    out.append(prev_le)
                else:
                    frac = 0 if c == prev_c else (target - prev_c) / (c - prev_c)
                    out.append(prev_le + frac * (le - prev_le))
                break
            prev_le, prev_c = le, c
    return out


def gauge(series_list, t0, t1, name):
    vals = [total(m, name) for ts, m in series_list if t0 <= ts <= t1]
    return (sum(vals) / len(vals), max(vals)) if vals else (None, None)


def fmt(v, d=0, scale=1.0):
    return "-" if v is None else f"{v * scale:.{d}f}"


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("run_dir")
    p.add_argument("--window", type=float, default=10)
    p.add_argument("--max-running", default="hq-east=128,hq-west=128,factory=64,retail=64")
    a = p.parse_args()
    cap = {k: float(v) for k, v in (x.split("=") for x in a.max_running.split(","))}
    samples = load(a.run_dir)
    sites = sorted({s for s, _ in samples})
    report = json.load(open(os.path.join(a.run_dir, "guidellm.json")))
    streaming = lambda l: l.get("streaming", "true") == "true"

    print("\n### Per site, per stage\n")
    print("| stage | site | done | share | running mean/peak | waiting mean/peak | rho | prefix hit | engine TTFT p50/p90 ms "
          "| ITL mean ms | EPP TTFT p50/p90 ms | EPP TPOT ms | in/cached tok | EPP errors |")
    print("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
    for b in report["benchmarks"]:
        s = b["config"]["strategy"]
        stage = f"{s.get('type_')} {s['rate']:.2f}/s" if s.get("rate") is not None else s.get("type_")
        if s.get("streams") is not None:
            stage = f"{s.get('type_')} {s['streams']}"
        t0, t1 = b["start_time"], b["end_time"]
        done = {x: delta(samples.get((x, "vllm"), []), t0, t1, "vllm:request_success_total") or 0 for x in sites}
        all_done = sum(done.values()) or 1
        for x in sites:
            v, e = samples.get((x, "vllm"), []), samples.get((x, "epp"), [])
            run_mean, run_peak = gauge(v, t0, t1, "vllm:num_requests_running")
            wait_mean, wait_peak = gauge(v, t0, t1, "vllm:num_requests_waiting")
            hits = delta(v, t0, t1, "vllm:prefix_cache_hits_total")
            queries = delta(v, t0, t1, "vllm:prefix_cache_queries_total")
            ttft = hist_quantiles(v, t0, t1, "vllm:time_to_first_token_seconds")
            itl_s, itl_c = delta(v, t0, t1, "vllm:inter_token_latency_seconds_sum"), delta(v, t0, t1, "vllm:inter_token_latency_seconds_count")
            ettft = hist_quantiles(e, t0, t1, "llm_d_epp_request_ttft_seconds", match=streaming) if e else [None, None]
            tp_s, tp_c = (delta(e, t0, t1, "llm_d_epp_request_tpot_seconds_sum"), delta(e, t0, t1, "llm_d_epp_request_tpot_seconds_count")) if e else (None, None)
            in_s, in_c = (delta(e, t0, t1, "llm_d_epp_request_input_tokens_sum"), delta(e, t0, t1, "llm_d_epp_request_input_tokens_count")) if e else (None, None)
            ca_s, ca_c = (delta(e, t0, t1, "llm_d_epp_request_cached_tokens_sum"), delta(e, t0, t1, "llm_d_epp_request_cached_tokens_count")) if e else (None, None)
            errs = delta(e, t0, t1, "llm_d_epp_request_error_total") if e else None
            ratio = lambda n, d: None if n is None or not d else n / d
            print(f"| {stage} | {x} | {done[x]:.0f} | {done[x] / all_done:.2f} | {fmt(run_mean, 1)}/{fmt(run_peak)} "
                  f"| {fmt(wait_mean, 1)}/{fmt(wait_peak)} | {fmt(None if run_mean is None else run_mean / cap.get(x, 1), 2)} "
                  f"| {fmt(ratio(hits, queries), 2)} | {fmt(ttft[0], 0, 1000)}/{fmt(ttft[1], 0, 1000)} "
                  f"| {fmt(ratio(itl_s, itl_c), 1, 1000)} | {fmt(ettft[0], 0, 1000)}/{fmt(ettft[1], 0, 1000)} "
                  f"| {fmt(ratio(tp_s, tp_c), 1, 1000)} | {fmt(ratio(in_s, in_c))}/{fmt(ratio(ca_s, ca_c))} | {fmt(errs)} |")

    start = min(b["start_time"] for b in report["benchmarks"])
    end = max(b["end_time"] for b in report["benchmarks"])
    print(f"\n### Share in {a.window:g}s windows (completions per site)\n")
    print("| t+s | " + " | ".join(sites) + " | total |")
    print("|---|" + "---|" * (len(sites) + 1))
    t = start
    while t < end:
        d = {x: delta(samples.get((x, "vllm"), []), t, t + a.window, "vllm:request_success_total") or 0 for x in sites}
        tot = sum(d.values())
        if tot:
            print(f"| {t - start:.0f} | " + " | ".join(f"{d[x] / tot:.2f}" for x in sites) + f" | {tot:.0f} |")
        t += a.window


if __name__ == "__main__":
    main()
