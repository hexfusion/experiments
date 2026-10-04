#!/usr/bin/env python3
"""Apply the cross-run routing gates to partition-sim reports from several rules.

Each argument is RULE=DIR, a GRID_SIM_REPORT_DIR holding *.metrics.json files. The first rule
is the baseline (LOR with every site tied).

  Row 16a/16b: p95 request latency at most 1.1x the baseline's.
  Row 20: requests served at least 0.9x the best rule's.

  compare_rules.py lor=DIR head=DIR p2c=DIR
"""

import glob
import json
import os
import sys

runs = [arg.split("=", 1) for arg in sys.argv[1:]]
metrics = {}
for rule, directory in runs:
    for path in glob.glob(os.path.join(directory, "**", "*.metrics.json"), recursive=True):
        key = os.path.basename(path)[: -len(".metrics.json")]
        metrics.setdefault(key, {})[rule] = json.load(open(path))

baseline = runs[0][0]
rules = [rule for rule, _ in runs]
print(f"| scenario | gate | {' | '.join(rules)} |")
print("|---|---|" + "---|" * len(rules))
for key in sorted(metrics):
    by_rule = metrics[key]
    if key.startswith("row16") and baseline in by_rule:
        bound = 1.1 * by_rule[baseline]["p95_seconds"]
        cells = []
        for rule in rules:
            p95 = by_rule.get(rule, {}).get("p95_seconds")
            cells.append("-" if p95 is None else f"{p95:.3f}s {'ok' if p95 <= bound else 'GAP'}")
        print(f"| {key} | p95 <= {bound:.3f}s | {' | '.join(cells)} |")
    if key.startswith("row20"):
        served = {rule: by_rule[rule]["requests_served"] for rule in rules if rule in by_rule}
        floor = 0.9 * max(served.values())
        cells = [
            "-" if rule not in served else f"{served[rule]:.0f} {'ok' if served[rule] >= floor else 'GAP'}"
            for rule in rules
        ]
        print(f"| {key} | served >= {floor:.0f} | {' | '.join(cells)} |")
