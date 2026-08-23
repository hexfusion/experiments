#!/usr/bin/env python3
"""Fan out ground-truth targets across every site's datasource.

A site's Prometheus scrapes only its own endpoint picker, so a panel that reads
`job="epp"` through the site selector can show exactly one pool no matter what
its title promises. The rest of the legend renders empty, which looks like a
pool sitting idle rather than a pool nobody asked about.

Any target marked `gridAllSites` is replaced by one target per site, each
pinned to that site's datasource, and its panel is switched to the mixed
datasource. Those panels then stop following the selector, which is the point:
they are grid-wide facts. Everything else stays on `${site}`, because poll
outcomes, retries and scrape health are statements about one observer.

The site list is passed in rather than baked into the dashboard, so adding a
site to the demo does not mean editing panel JSON.
"""
import json
import sys
from collections import OrderedDict

MIXED = {"type": "datasource", "uid": "-- Mixed --"}
# Grafana identifies targets within a panel by refId; duplicates make it drop
# all but one, which would silently show a single pool again.
REF_IDS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"


def panels(nodes):
    for node in nodes:
        yield node
        if node.get("panels"):
            yield from panels(node["panels"])


def expand(dashboard, sites):
    for panel in panels(dashboard.get("panels", [])):
        targets = panel.get("targets")
        if not targets or not any(t.get("gridAllSites") for t in targets):
            continue
        out = []
        for target in targets:
            if target.pop("gridAllSites", False):
                for site in sites:
                    copy = OrderedDict(target)
                    copy["datasource"] = {"type": "prometheus", "uid": site}
                    out.append(copy)
            else:
                out.append(target)
        for i, target in enumerate(out):
            target["refId"] = REF_IDS[i % len(REF_IDS)]
        panel["targets"] = out
        panel["datasource"] = MIXED
    return dashboard


def main():
    if len(sys.argv) < 3:
        sys.exit("usage: expand-dashboard.py <dashboard.json> <site> [site...]")
    path, sites = sys.argv[1], sys.argv[2:]
    with open(path) as fh:
        dashboard = json.load(fh, object_pairs_hook=OrderedDict)
    json.dump(expand(dashboard, sites), sys.stdout, indent=2)


if __name__ == "__main__":
    main()
