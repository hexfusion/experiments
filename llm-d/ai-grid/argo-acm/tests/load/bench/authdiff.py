#!/usr/bin/env python3
"""Diff two authstats.sh snapshots: wasm-shim errors and Authorino Check latency quantiles.

  authdiff.py BEFORE.json AFTER.json
"""
import json, sys

a, b = (json.load(open(f)) for f in sys.argv[1:3])
w = {k: b["wasm"].get(k, 0) - a["wasm"].get(k, 0) for k in ("allowed", "denied", "errors", "hits", "misses")}
buckets = sorted(((float(k[7:]), b["check"][k] - a["check"].get(k, 0)) for k in b["check"] if k.startswith("bucket:")), key=lambda x: x[0])
count = b["check"].get("count", 0) - a["check"].get("count", 0)
total = b["check"].get("sum", 0) - a["check"].get("sum", 0)


def q(p):
    target = p * count
    for le, c in buckets:
        if c >= target:
            return le
    return float("inf")


print(f"wasm-shim: allowed {w['allowed']:.0f}, denied {w['denied']:.0f}, errors {w['errors']:.0f} "
      f"({w['errors'] / max(1, w['allowed'] + w['errors']):.1%} of checks)")
print(f"Authorino Check: {count:.0f} calls, mean {1000 * total / max(1, count):.0f} ms, "
      f"p50 <= {1000 * q(0.5):.0f} ms, p90 <= {1000 * q(0.9):.0f} ms, p99 <= {1000 * q(0.99):.0f} ms (bucket bounds)")
