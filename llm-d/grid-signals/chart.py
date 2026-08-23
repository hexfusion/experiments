"""Chart a scenario run from each site's own Prometheus.

Reads the phase boundaries the runner wrote and draws them as bands, because a
queue that rose and a queue that was pushed look identical without them.

Every series comes from the site that observed it, which is the point: the same
quantity appears once as a pool measured itself and again as each peer holds it,
and the gap between those lines is what a routing decision inherits.
"""

import json
import subprocess
import sys
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

HERE = Path(__file__).resolve().parent
SITES = ["pool-a", "pool-b", "pool-c"]
CTX = "kind-grid-llmd-pm-{}"


def query_range(site, expr, start, end, step=5):
    """Range-query one site's Prometheus through the API server proxy."""
    import urllib.parse

    path = (
        f"/api/v1/namespaces/grid-system/services/prometheus:9090/proxy"
        f"/api/v1/query_range?query={urllib.parse.quote(expr)}"
        f"&start={start}&end={end}&step={step}"
    )
    out = subprocess.run(
        ["kubectl", "--context", CTX.format(site), "get", "--raw", path],
        capture_output=True, text=True, check=False,
    )
    if out.returncode != 0:
        return []
    try:
        return json.loads(out.stdout)["data"]["result"]
    except (KeyError, json.JSONDecodeError):
        return []


def series(result, label_keys):
    """Flatten a Prometheus result into (label, xs, ys)."""
    for r in result:
        m = r["metric"]
        label = " ".join(str(m.get(k, "")) for k in label_keys).strip() or "value"
        xs = [float(p[0]) for p in r["values"]]
        ys = [float("nan") if p[1] in ("NaN", "+Inf") else float(p[1]) for p in r["values"]]
        yield label, xs, ys


def bands(ax, phases, t0):
    """Shade each phase and name it once, at the top."""
    colours = {"baseline": "#eef4ff", "load": "#fff4e5", "blackhole": "#ffe9e9", "healed": "#eafaf0"}
    for i, p in enumerate(phases[:-1]):
        start, end = p["at"] - t0, phases[i + 1]["at"] - t0
        key = next((k for k in colours if p["phase"].startswith(k)), "baseline")
        ax.axvspan(start, end, color=colours[key], zorder=0)
        ax.axvline(start, color="#c8ced8", lw=0.8, zorder=1)


def main(phase_file):
    phases = [json.loads(l) for l in Path(phase_file).read_text().splitlines() if l.strip()]
    if len(phases) < 2:
        print("not enough phases recorded", file=sys.stderr)
        return 1
    t0, t1 = phases[0]["at"], phases[-1]["at"]

    panels = [
        # Every site, not one. Each Prometheus scrapes only the pool beside it,
        # so asking a single site for "each pool" returns exactly one line and
        # silently omits the pool the load was aimed at.
        # Named for what it is. Load enters at one gateway and the router
        # spreads it, so this is where work landed rather than where it was
        # offered, and reading it as the latter is how the last run got
        # misread.
        ("Queue depth",
         "llm_d_epp_average_queue_size{job='epp'}", ["site"], None, "requests"),
        ("Held copy",
         "llm_d_epp_average_queue_size{job='signals'}", ["observer", "grid_site"], None, "requests"),
        ("Sample age",
         "time() - timestamp(llm_d_epp_average_queue_size{job='signals'})",
         ["observer", "grid_site"], None, "seconds"),
        ("Peer availability", "grid_collection_up", ["peer"], None, "1 = yes"),
        ("Poll outcomes", "sum by (peer, outcome) (rate(grid_peer_poll_total[30s]))",
         ["peer", "outcome"], None, "per second"),
        ("Poll duration",
         "histogram_quantile(0.9, sum by (le,peer) (rate(grid_peer_poll_duration_seconds_bucket[1m])))",
         ["peer"], None, "seconds"),
    ]

    fig, axes = plt.subplots(len(panels), 1, figsize=(13, 3.1 * len(panels)), sharex=True)
    for ax, (title, expr, keys, site, unit) in zip(axes, panels):
        for src in ([site] if site else SITES):
            for label, xs, ys in series(query_range(src, expr, t0, t1), keys):
                # A ground-truth series already names its own site, so
                # prefixing it with the site that reported it would say the
                # same word twice. A held series needs both: who holds it and
                # who it is about.
                shown = label if (site or label == src) else f"{src}: {label}"
                ax.plot([x - t0 for x in xs], ys, lw=1.5, label=shown)
        bands(ax, phases, t0)
        ax.set_title(title, loc="left", fontsize=11, fontweight="bold")
        ax.set_ylabel(unit, fontsize=9)
        ax.grid(alpha=0.25, lw=0.5)
        handles, _ = ax.get_legend_handles_labels()
        if handles:
            ax.legend(fontsize=7, ncol=3, loc="upper left", framealpha=0.9)

    for p in phases[:-1]:
        axes[0].annotate(p["phase"], xy=(p["at"] - t0, 1.02), xycoords=("data", "axes fraction"),
                         fontsize=8, rotation=0, ha="left", color="#334")
    axes[-1].set_xlabel("seconds since the run started", fontsize=9)
    fig.suptitle("Grid signals under load and partition", x=0.02, ha="left",
                 fontsize=14, fontweight="bold")
    fig.tight_layout(rect=(0, 0, 1, 0.985))

    out = HERE / ".generated" / "scenario.png"
    fig.savefig(out, dpi=130)
    print(f"wrote {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else HERE / ".generated" / "phases.json"))
