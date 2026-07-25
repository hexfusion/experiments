#!/usr/bin/env python3
"""Plot sweep.sh output: HTTP vs ext_proc across a concurrency ramp.

Reads CSV on stdin, writes an SVG. Deliberately plain: two lines per panel, log
concurrency axis, no gridline decoration competing with the data.
"""
import csv
import math
import sys

W, H = 1160, 900
PAD_L, PAD_R, PAD_T, PAD_B = 78, 24, 40, 52
COLS, ROWS = 2, 2
PW = (W - PAD_L - PAD_R - 60) // COLS
PH = (H - PAD_T - PAD_B - 90) // ROWS

# http is the thing under test, ext_proc is the incumbent it is measured against.
COLOR = {"http": "#3B82F6", "ext_proc": "#F59E0B"}


def read():
    rows = list(csv.DictReader(sys.stdin))
    # Whichever of concurrency or body_kb actually moves is the x axis.
    xkey = "concurrency"
    if "body_kb" in rows[0] and len({r["body_kb"] for r in rows}) > 1:
        xkey = "body_kb"
    data = {}
    for r in rows:
        data.setdefault(r["transport"], []).append(r)
    for v in data.values():
        v.sort(key=lambda r: int(r[xkey]))
    return data, xkey


def nice_max(v):
    if v <= 0:
        return 1.0
    e = 10 ** math.floor(math.log10(v))
    for m in (1, 1.5, 2, 3, 5, 7.5, 10):
        if m * e >= v:
            return m * e
    return 10 * e


def panel(out, data, xkey, key, title, unit, ox, oy, lower_better=True):
    concs = sorted({int(r[xkey]) for rs in data.values() for r in rs})
    vmax = nice_max(max(float(r[key]) for rs in data.values() for r in rs))
    lx = [math.log10(c) for c in concs]
    x0, x1 = min(lx), max(lx)

    def px(c):
        if x1 == x0:
            return ox + PW / 2
        return ox + (math.log10(c) - x0) / (x1 - x0) * PW

    def py(v):
        return oy + PH - (v / vmax) * PH

    out.append(f'<text x="{ox}" y="{oy-14}" font-size="14.5" font-weight="700" fill="#0F172A">{title}</text>')
    out.append(f'<rect x="{ox}" y="{oy}" width="{PW}" height="{PH}" fill="#F8FAFC" stroke="#CBD5E1" stroke-width="1" rx="6"/>')

    for i in range(5):
        v = vmax * i / 4
        y = py(v)
        out.append(f'<line x1="{ox}" y1="{y:.1f}" x2="{ox+PW}" y2="{y:.1f}" stroke="#E2E8F0" stroke-width="1"/>')
        lab = f"{v:.0f}" if vmax >= 10 else f"{v:.2f}"
        out.append(f'<text x="{ox-8}" y="{y+4:.1f}" font-size="11.5" fill="#64748B" text-anchor="end">{lab}</text>')
    out.append(f'<text x="{ox-56}" y="{oy+PH/2}" font-size="11.5" fill="#64748B" transform="rotate(-90 {ox-56} {oy+PH/2})" text-anchor="middle">{unit}</text>')

    for c in concs:
        x = px(c)
        out.append(f'<text x="{x:.1f}" y="{oy+PH+18}" font-size="11.5" fill="#64748B" text-anchor="middle">{c}</text>')

    for name, rs in data.items():
        pts = " ".join(f"{px(int(r[xkey])):.1f},{py(float(r[key])):.1f}" for r in rs if float(r[key]) > 0 or key == "errors")
        col = COLOR.get(name, "#64748B")
        out.append(f'<polyline points="{pts}" fill="none" stroke="{col}" stroke-width="2.5"/>')
        for r in rs:
            v = float(r[key])
            x, y = px(int(r[xkey])), py(v)
            if v <= 0 and int(r["errors"]) > 0:
                # A failed arm is not a zero: mark it and do not draw a line to it.
                out.append(f'<text x="{x:.1f}" y="{oy+PH-8:.1f}" font-size="16" font-weight="700" fill="#DC2626" text-anchor="middle">x</text>')
                continue
            out.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="4" fill="{col}"/>')

    # Ratio at the widest concurrency, since that is where the arms diverge most.
    if len(data) == 2 and all(data.values()):
        a, b = data.get("http"), data.get("ext_proc")
        if a and b:
            va, vb = float(a[-1][key]), float(b[-1][key])
            if vb and va:
                r = va / vb
                better = (r < 1) if lower_better else (r > 1)
                verdict = "http better" if better else "ext_proc better"
                out.append(f'<text x="{ox+PW-8}" y="{oy+16}" font-size="12" font-weight="700" fill="#334155" text-anchor="end">{r:.2f}x at {a[-1][xkey]} · {verdict}</text>')


def main():
    data, xkey = read()
    if not data:
        sys.exit("no rows")
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" font-family="Helvetica, Arial, sans-serif">',
           f'<rect width="{W}" height="{H}" fill="#ffffff"/>']

    cells = [
        ("p50_ms", "Decision latency p50 (adds to TTFT)", "ms", True),
        ("p99_ms", "Decision latency p99", "ms", True),
        ("rps", "Throughput", "req/s", False),
        ("epp_mem_mib", "EPP memory while serving", "MiB", True),
    ]
    for i, (key, title, unit, lower) in enumerate(cells):
        ox = PAD_L + (i % COLS) * (PW + 60)
        oy = PAD_T + 24 + (i // COLS) * (PH + 90)
        panel(out, data, xkey, key, title, unit, ox, oy, lower)

    ly = H - 22
    for i, (name, col) in enumerate(COLOR.items()):
        x = PAD_L + i * 150
        out.append(f'<rect x="{x}" y="{ly-11}" width="22" height="4" rx="2" fill="{col}"/>')
        out.append(f'<text x="{x+30}" y="{ly-5}" font-size="13" font-weight="700" fill="#334155">{name}</text>')
    axis = "body size (KB)" if xkey == "body_kb" else "concurrency"
    out.append(f'<text x="{W-PAD_R}" y="{ly-5}" font-size="11.5" fill="#94A3B8" text-anchor="end">same EPP · x axis is {axis}, log scale · red x = arm failed</text>')
    out.append("</svg>")
    sys.stdout.write("\n".join(out) + "\n")


main()
