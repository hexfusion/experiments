#!/usr/bin/env python3
"""k6 routing graphs: llm-d vs baseline, one metric per chart. Reads the single-run summaries
data/llm-d.json + data/baseline.json (no aggregation)."""
import json, os, sys
import matplotlib; matplotlib.use("Agg")
import matplotlib.pyplot as plt

DATA = sys.argv[1] if len(sys.argv) > 1 else "data"
OUT  = sys.argv[2] if len(sys.argv) > 2 else "graphs"
os.makedirs(OUT, exist_ok=True)

def load(n):
    p = os.path.join(DATA, n + ".json")
    return json.load(open(p)) if os.path.exists(p) and os.path.getsize(p) else None

llmd, base = load("llm-d"), load("baseline")
if not llmd and not base:
    sys.exit("no data in %s" % DATA)
BLUE, ORANGE = "#1f77b4", "#ff7f0e"

def bars(title, ylabel, lval, bval, fname, fmt):
    fig, ax = plt.subplots(figsize=(4.6, 4.0))
    ax.bar(["baseline", "llm-d"], [bval, lval], color=[ORANGE, BLUE], width=0.55)
    for i, v in enumerate([bval, lval]):
        ax.text(i, v, fmt % v, ha="center", va="bottom", fontsize=14)
    ax.set_title(title, fontsize=19, fontweight="bold"); ax.set_ylabel(ylabel)
    ax.margins(y=0.22); ax.spines[["top", "right"]].set_visible(False); ax.tick_params(labelsize=12)
    fig.tight_layout(); fig.savefig(os.path.join(OUT, fname), dpi=130); plt.close(fig)
    print(" ", fname)

def pct_loaded(d):
    # first cluster is the heated one (run.sh loads CLUSTERS[0])
    ns = [(v or {}).get("n") or 0 for v in (d.get("clusters") or {}).values()]
    tot = sum(ns)
    return 100.0 * ns[0] / tot if tot and ns else 0

def p50(d):
    return (d.get("overall") or {}).get("p50") or 0

if (llmd or base or {}).get("mode") == "affinity":
    bars("Affinity", "session stickiness (%)",
         (llmd or {}).get("stickiness_pct") or 0, (base or {}).get("stickiness_pct") or 0, "affinity.png", "%d%%")
else:
    bars("Load shed", "traffic to loaded cluster (%)", pct_loaded(llmd or {}), pct_loaded(base or {}), "load.png", "%d%%")
    bars("Served latency", "overall p50 (ms)", p50(llmd or {}), p50(base or {}), "latency.png", "%.0f ms")
print("done ->", OUT)
