#!/usr/bin/env python3
"""One markdown row per sweep step of a guidellm JSON report.

  summarize.py REPORT.json LABEL
"""

import collections
import json
import re
import sys

report = json.load(open(sys.argv[1]))
label = sys.argv[2]


def stat(metrics, name, pct, scale=1.0):
    s = (metrics.get(name) or {}).get("successful") or {}
    value = s.get("median") if pct == "p50" else (s.get("percentiles") or {}).get(pct)
    return None if value is None else value * scale


def fmt(value, digits=0):
    return "-" if value is None else f"{value:.{digits}f}"


def statuses(benchmark):
    """Errored requests counted by the HTTP status in their error text, else by error kind."""
    counts = collections.Counter()
    for request in (benchmark.get("requests") or {}).get("errored") or []:
        text = json.dumps(request)
        match = re.search(r"\b([45]\d\d)\b", text)
        counts[match.group(1) if match else "no-status"] += 1
    return " ".join(f"{code}:{n}" for code, n in sorted(counts.items())) or "-"


print(f"| run | step | ok | errored | incomplete | req/s | out tok/s | TTFT p50/p95/p99 ms | ITL p50/p95/p99 ms "
      f"| e2e p50/p95/p99 s | errors |")
print("|---|---|---|---|---|---|---|---|---|---|---|")
for benchmark in report["benchmarks"]:
    strategy = benchmark["config"]["strategy"]
    step = strategy.get("type_")
    if strategy.get("rate") is not None:
        step = f"{step} {strategy['rate']:.2f}/s"
    m = benchmark["metrics"]
    totals = m["request_totals"]
    triple = lambda name, digits, scale=1.0: "/".join(fmt(stat(m, name, p, scale), digits) for p in ("p50", "p95", "p99"))
    print(
        f"| {label} | {step} | {totals['successful']} | {totals['errored']} | {totals['incomplete']} "
        f"| {fmt((m['requests_per_second'].get('successful') or {}).get('mean'), 2)} "
        f"| {fmt((m['output_tokens_per_second'].get('successful') or {}).get('mean'), 0)} "
        f"| {triple('time_to_first_token_ms', 0)} | {triple('inter_token_latency_ms', 1)} "
        f"| {triple('request_latency', 2)} | {statuses(benchmark)} |"
    )
