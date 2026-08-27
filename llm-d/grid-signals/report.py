#!/usr/bin/env python3
"""Render a demo run's evidence as a self-contained HTML report.

Reads the evidence.json a run writes and emits one file with the data
inlined, so a result can be handed to someone without the cluster, the
repo, or a Grafana behind it.

  ./report.py .generated/evidence/<run>/evidence.json -o report.html

Runs before the timeline was recorded still render: the quota chart falls
back to parsing what the rate limit proof observed, and the routing chart
says it has nothing rather than drawing something it does not have.
"""
import argparse, json, pathlib, re, sys

ORDER = ["provenance", "baseline", "signals", "pressure_and_flip", "recovery",
         "load_drives_routing", "per_identity_rate_limit"]

# What each proof is actually claiming, in the reader's terms.
CLAIMS = {
    "provenance": "The images and model under test are the ones named",
    "baseline": "At rest, the nearest site is preferred",
    "signals": "Every site publishes attributed, timestamped load and says who it can reach",
    "pressure_and_flip": "Load on one site moves traffic to another",
    "recovery": "When the load stops, preference returns",
    "load_drives_routing": "The polled signal is what decides, shown by withdrawing it",
    "per_identity_rate_limit": "Each tenant is held to the limit its own token carries",
}

def parse_quota_fallback(proofs):
    """Recover quota outcomes from observations, for runs recorded before
    the structured field existed.

    The limits are deliberately not filled in. They live in the realm, they
    change between runs, and stamping today's numbers onto an older run
    would draw a burst line that run never had.
    """
    p = proofs.get("per_identity_rate_limit")
    if not p:
        return []
    out = []
    for obs in p.get("observations", []):
        m = re.match(r"^(\S+): (\d+) at once -> (.+)$", obs)
        if not m:
            continue
        tenant, offered, tally = m.group(1), int(m.group(2)), m.group(3)
        counts = {c: int(n) for n, c in re.findall(r"(\d+)x(\d+)", tally)}
        other = {k: v for k, v in counts.items() if k not in ("200", "429")}
        out.append({"tenant": tenant, "rate": None, "burst": None,
                    "offered": offered, "served": counts.get("200", 0),
                    "throttled": counts.get("429", 0), "other": other})
    return out

def build(ev):
    proofs = ev.get("proofs", {})
    quotas = ev.get("quotas") or parse_quota_fallback(proofs)
    ordered = [k for k in ORDER if k in proofs] + [k for k in sorted(proofs) if k not in ORDER]
    return {
        "run": {
            "started_at": ev.get("started_at"),
            "wall_secs": ev.get("wall_secs"),
            "success": ev.get("success"),
            "mode": ev.get("mode"),
            "transport": ev.get("metrics_transport"),
            "scoring": ev.get("scoring_strategy"),
            "error": ev.get("error"),
            "images": (ev.get("setup") or {}).get("images") or {},
            "clusters": (ev.get("setup") or {}).get("clusters") or [],
        },
        "proofs": [{"name": k, "claim": CLAIMS.get(k, ""), **proofs[k]} for k in ordered],
        "timeline": ev.get("timeline") or [],
        "quotas": quotas,
    }

TEMPLATE = pathlib.Path(__file__).with_name("report-template.html")

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("evidence")
    ap.add_argument("-o", "--out", default="report.html")
    a = ap.parse_args()
    ev = json.load(open(a.evidence))
    data = build(ev)
    html = TEMPLATE.read_text().replace(
        "/*__DATA__*/null", json.dumps(data, separators=(",", ":")))
    pathlib.Path(a.out).write_text(html)
    t, q = len(data["timeline"]), len(data["quotas"])
    print(f"{a.out}: {len(data['proofs'])} proofs, {t} timeline ticks, {q} quota outcomes")
    if not t:
        print("  (no timeline in this run; the routing chart will say so)", file=sys.stderr)

if __name__ == "__main__":
    main()
