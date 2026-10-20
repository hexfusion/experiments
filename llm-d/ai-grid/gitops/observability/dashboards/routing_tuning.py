#!/usr/bin/env python3
"""Render the AI Grid routing tuning dashboard as an ACM Grafana ConfigMap.

Design: hexfusion/design work/llm-d/ai-grid/ROUTING-DASHBOARD.md.
Usage: python3 routing_tuning.py > ../acm/65-dashboard-routing-tuning.yaml
"""

import json
import sys

DS = {"type": "prometheus", "uid": "${datasource}"}
# Hub samples arrive every 60 s. Grafana's $__rate_interval is the panel interval plus the
# datasource scrape interval, so a 2m floor keeps every rate window over at least two samples.
INTERVAL = "2m"
GW = 'cluster=~"$cluster",site=~"$site"'
POOL = 'cluster=~"$cluster",namespace=~"$pool"'

# A short candidate name for legends, keeping the full backend when the name has no hash suffix.
def cand(expr):
    full = f'label_replace({expr}, "candidate", "$1", "backend", "(.*)")'
    return f'label_replace({full}, "candidate", "$1", "backend", "grid-(.+)-[^-]+-[^-]+-[0-9a-f]{{16,}}")'


# Why a learned-path gauge is empty: the gateway sets it only for a candidate it learns from.
LEARNED = (" Empty (NaN) until the gateway learns this candidate's capacity, which it does only when "
           "the operator relays grid_provider_config_generation with provider_uid, publisher_epoch, "
           "batch_sequence and batch_samples: an operator built from grid #386 or later.")


# The gateway's own response counter, per candidate: its cluster label arrives as backend.
RESP = 'praxis_http_requests_total{job="grid-gateway-metrics",cluster=~"$cluster"}'
RESP_OK = RESP.replace("}", ',status_class="2xx"}')
RESP_ERR = RESP.replace("}", ',status_class=~"4xx|5xx"}')
DEC = 'grid_route_decisions_total{cluster=~"$cluster",site=~"$site",reason=~"routed|fallback"}'
# A provider-hop gateway (for example site-e's) answers for requests another gateway decided and
# makes no decisions of its own. Response panels keep only candidates a decider picked.
PICKED = 'group by (cluster, backend) (grid_route_decisions_total{cluster=~"$cluster"})'


def picked(expr):
    return f"(({expr}) and on (cluster, backend) {PICKED})"


def count(series, by):
    """Exact count over the dashboard range, per series then summed.

    increase() extrapolates to the range edges and drops what a series held at its first sample,
    which skews short or new series such as a restarted gateway pod's. A counter here never resets
    within one series (each pod has its own), so a series present at the range start counts
    max minus min, and one that started inside the range counts its max.
    """
    r = "$__range"
    span = f"(max_over_time({series}[{r}]) - min_over_time({series}[{r}]))"
    return (f"sum by ({by}) (({span} and ({series} offset {r})) or "
            f"(max_over_time({series}[{r}]) unless ({series} offset {r})))")


def rate(metric, sel, by):
    return f"sum by ({by}) (rate({metric}{{{sel}}}[$__rate_interval]))"


# ACM names the hub's own cluster local-cluster; legends show the grid site instead.
HUB_SITE = "dagobah"


def at_site(expr):
    """Copy cluster into an "at" label, renaming the hub to its site, without touching site."""
    at = f'label_replace({expr}, "at", "$1", "cluster", "(.+)")'
    return f'label_replace({at}, "at", "{HUB_SITE}", "cluster", "local-cluster")'


def q(expr, legend, ref="A", instant=False):
    # Name where a per-cluster or per-pool series lives by site, so a remote pool never reads as local.
    if "{{cluster}}" in legend or "namespace}}" in legend:
        expr = at_site(expr)
        legend = legend.replace("{{cluster}} / ", "").replace("{{cluster}}", "{{at}}")
        if "{{at}}" not in legend:
            legend = "{{at}} / " + legend
    return {"datasource": DS, "expr": expr, "legendFormat": legend, "refId": ref,
            "range": not instant, "instant": instant}


class Board:
    def __init__(self):
        self.panels, self.y, self.id = [], 0, 0

    def _id(self):
        self.id += 1
        return self.id

    def row(self, title):
        self.panels.append({"type": "row", "title": title, "id": self._id(), "collapsed": False,
                            "gridPos": {"h": 1, "w": 24, "x": 0, "y": self.y}, "panels": []})
        self.y += 1
        self.x = 0
        self.h = 0

    def _place(self, w, h):
        if self.x + w > 24:
            self.y += self.h
            self.x, self.h = 0, 0
        pos = {"h": h, "w": w, "x": self.x, "y": self.y}
        self.x += w
        self.h = max(self.h, h)
        return pos

    def end_row(self):
        # A panel alone on any line of the row takes the full width, so no line has a hole.
        start = max(p["gridPos"]["y"] for p in self.panels if p["type"] == "row")
        lines = {}
        for p in self.panels:
            if p["type"] != "row" and p["gridPos"]["y"] > start:
                lines.setdefault(p["gridPos"]["y"], []).append(p)
        for line in lines.values():
            if len(line) == 1 and line[0]["gridPos"]["w"] < 24:
                line[0]["gridPos"].update(x=0, w=24)
        self.y += self.h
        self.x, self.h = 0, 0

    def ts(self, title, desc, targets, unit="short", w=12, h=10, stack=False, overrides=None,
           mappings=None, minv=None, maxv=None, novalue="no data"):
        custom = {"lineWidth": 1, "fillOpacity": 25 if stack else 0, "spanNulls": False,
                  "stacking": {"mode": "normal" if stack else "none"}}
        defaults = {"unit": unit, "noValue": novalue, "custom": custom, "mappings": mappings or []}
        if minv is None and unit == "percentunit":
            minv = 0
        if maxv is None and unit == "percentunit":
            # Saturation can pass 100%, so a soft ceiling: 0 to 100% unless a value goes beyond.
            custom["axisSoftMax"] = 1
        if minv is not None:
            defaults["min"] = minv
        if maxv is not None:
            defaults["max"] = maxv
        self.panels.append({
            "type": "timeseries", "title": title, "description": desc, "id": self._id(),
            "datasource": DS, "interval": INTERVAL, "gridPos": self._place(w, h),
            "fieldConfig": {"defaults": defaults, "overrides": overrides or []},
            "options": {"legend": {"displayMode": "table", "placement": "bottom", "showLegend": True,
                                   "calcs": ["lastNotNull", "mean"]},
                        "tooltip": {"mode": "multi", "sort": "desc"}},
            "targets": targets})

    def stat(self, title, desc, expr, unit="short", w=3, decimals=2, thresholds=None):
        self.panels.append({
            "type": "stat", "title": title, "description": desc, "id": self._id(),
            "datasource": DS, "interval": INTERVAL, "gridPos": self._place(w, 4),
            "fieldConfig": {"defaults": {"unit": unit, "decimals": decimals, "noValue": "none",
                                         "thresholds": thresholds or {"mode": "absolute", "steps": [
                                             {"color": "text", "value": None}]}},
                            "overrides": []},
            "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                        "colorMode": "value", "graphMode": "area", "textMode": "auto"},
            "targets": [q(expr, "")]})

    def table(self, title, desc, columns, w=24, h=7):
        """columns: (name, expr, unit). Instant queries merged on their labels, one row per candidate."""
        targets, rename, overrides = [], {}, []
        for i, (name, expr, unit) in enumerate(columns):
            ref = chr(ord("A") + i)
            targets.append({"datasource": DS, "expr": expr, "refId": ref, "instant": True, "range": False,
                            "format": "table"})
            rename[f"Value #{ref}"] = name
            props = [{"id": "unit", "value": unit}, {"id": "decimals", "value": 1 if unit == "percentunit" else 0}]
            if name in ("Errors", "Aborted"):
                props += [{"id": "custom.cellOptions", "value": {"type": "color-text"}},
                          {"id": "thresholds", "value": {"mode": "absolute", "steps": [
                              {"color": "text", "value": None}, {"color": "red", "value": 1}]}}]
            if name == "Success":
                props += [{"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "basic"}},
                          {"id": "thresholds", "value": {"mode": "absolute", "steps": [
                              {"color": "red", "value": None}, {"color": "orange", "value": 0.9},
                              {"color": "green", "value": 0.99}]}}]
            overrides.append({"matcher": {"id": "byName", "options": name}, "properties": props})
        self.panels.append({
            "type": "table", "title": title, "description": desc, "id": self._id(), "datasource": DS,
            "gridPos": self._place(w, h), "targets": targets,
            "transformations": [
                {"id": "merge", "options": {}},
                {"id": "organize", "options": {
                    "excludeByName": {"Time": True, "cluster": True, "backend": True, "namespace": True},
                    "indexByName": {"candidate": 0, **{c[0]: i + 1 for i, c in enumerate(columns)}},
                    "renameByName": {**rename, "candidate": "Candidate"}}},
                {"id": "sortBy", "options": {"sort": [{"field": "Candidate"}]}}],
            "fieldConfig": {"defaults": {"noValue": "0", "custom": {"align": "auto"}}, "overrides": overrides},
            "options": {"showHeader": True, "cellHeight": "sm"}})

    def bars(self, title, desc, expr, legend, w=12, h=10):
        self.panels.append({
            "type": "bargauge", "title": title, "description": desc, "id": self._id(),
            "datasource": DS, "gridPos": self._place(w, h),
            "fieldConfig": {"defaults": {"unit": "percentunit", "min": 0, "max": 1, "decimals": 1,
                                         "noValue": "no decisions in range",
                                         "color": {"mode": "palette-classic"}},
                            "overrides": []},
            "options": {"orientation": "horizontal", "displayMode": "basic", "showUnfilled": True,
                        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
            "targets": [dict(q(expr, legend), instant=True, range=False)]})

    def state(self, title, desc, targets, w=24, h=5, mappings=None):
        self.panels.append({
            "type": "state-timeline", "title": title, "description": desc, "id": self._id(),
            "datasource": DS, "interval": INTERVAL, "gridPos": self._place(w, h),
            "fieldConfig": {"defaults": {"mappings": mappings or [], "noValue": "no data",
                                         "thresholds": {"mode": "absolute", "steps": [
                                             {"color": "green", "value": None},
                                             {"color": "red", "value": 1}]}},
                            "overrides": []},
            "options": {"showValue": "never", "mergeValues": True, "rowHeight": 0.8},
            "targets": targets})


def right(ref, dash=None):
    props = [{"id": "custom.axisPlacement", "value": "right"}]
    if dash:
        props += [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": dash}}]
    return {"matcher": {"id": "byFrameRefID", "options": ref}, "properties": props}


def unmeasured():
    return [{"type": "special", "options": {"match": "nan", "result": {"text": "unmeasured"}}}]


def dashed(ref, dash):
    return {"matcher": {"id": "byFrameRefID", "options": ref},
            "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": dash}},
                           {"id": "custom.lineWidth", "value": 2}]}


def build():
    b = Board()

    # Row 1: verdict.
    b.row("Verdict")
    decided = rate("grid_route_decisions_total", GW, "cluster")
    b.stat("Decided per second", "Requests the grid gateways decided, every reason.",
           f"sum({decided})", unit="reqps")
    refused = rate("grid_route_decisions_total", GW + ',reason=~"not_ready|no_route|bad_request|shed"', "cluster")
    b.stat("Refused", "Share of decisions that refused the request: not ready, no route, bad request or shed.",
           f"sum({refused}) / sum({decided})", unit="percentunit",
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "red", "value": 0.01}]})
    fb = rate("grid_route_decisions_total", GW + ',reason="fallback"', "cluster")
    b.stat("Fallback", "Share of decisions sent to a candidate only because no healthy one was left.",
           f"sum({fb}) / sum({decided})", unit="percentunit",
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "orange", "value": 0.01}]})
    b.stat("Oldest reading", "The oldest sample any candidate's decision read. A rising value is a frozen input." + LEARNED,
           f'max(grid_route_site_reading_age_seconds{{{GW}}})', unit="s", decimals=1,
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "orange", "value": 17},
                                                    {"color": "red", "value": 60}]})
    b.stat("Candidates full", "Candidates with a backlog at their ceiling long enough to shed." + LEARNED,
           f'count(grid_route_site_full{{{GW}}} == 1) or vector(0)', decimals=0,
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "red", "value": 1}]})
    b.stat("Models shedding", "Models the gateway sheds right now. The series appears with the first shed.",
           'count(grid_route_shedding{cluster=~"$cluster"} == 1) or vector(0)', decimals=0,
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "red", "value": 1}]})
    b.stat("Candidates failing",
           "How many candidates returned at least one error and not one 2xx in the last 10 minutes. "
           "A count over a rolling window, not a latch: it drops when the candidate succeeds again or "
           "its errors age out. Slow candidates do not count; requests still in flight are neither.",
           f"count(({picked(f'sum by (cluster, backend) (increase({RESP_ERR}[10m]))')} > 0) "
           f"unless on (cluster, backend) "
           f"(sum by (cluster, backend) (increase({RESP_OK}[10m])) > 0)) "
           "or vector(0)", decimals=0,
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "red", "value": 1}]})
    b.stat("Worst TTFT p95", "The slowest pool's p95 time to first token.",
           "max(histogram_quantile(0.95, sum by (le, cluster, namespace) "
           f'(rate(vllm:time_to_first_token_seconds_bucket{{{POOL}}}[$__rate_interval]))))',
           unit="s", decimals=2)
    b.end_row()

    # Row 2: where requests went.
    b.row("Where requests went")
    routed = 'grid_route_decisions_total{' + GW + ',reason=~"routed|fallback"}'
    per_cand = f"sum by (cluster, site, backend) (rate({routed}[$__rate_interval]))"
    b.ts("Routed per candidate", "Requests each gateway sent to each candidate, routed and fallback.",
         [q(cand(per_cand), "{{site}} / {{candidate}}")], unit="reqps", stack=True)
    achieved = f"{per_cand} / on (cluster) group_left sum by (cluster) ({per_cand})"
    # As the draw does: an unmeasured weight takes the mean of the known ones, or 1 when none is known.
    each = f'group by (cluster, site, backend) (grid_route_site_weight{{{GW}}})'
    known = f'avg by (cluster, site, backend) (grid_route_site_weight{{{GW}}} >= 0)'
    weight = (f"({known} or on (cluster, site, backend) ({each} * on (cluster) group_left "
              f"avg by (cluster) ({known})) or on (cluster, site, backend) {each})")
    rho = (f'avg by (cluster, site, backend) ((grid_route_site_rho{{{GW}}} >= 0) '
           f'or (grid_route_site_weight{{{GW}}} * 0))')
    cap_share = f"{weight} / on (cluster) group_left sum by (cluster) ({weight})"
    eff = f"({weight} / (1 + {rho}))"
    eff_share = f"{eff} / on (cluster) group_left sum by (cluster) ({eff})"
    b.ts("Achieved share against target",
         "Solid: the share each candidate got. Dashed: its capacity share, the by_capacity target. "
         "Dotted: capacity over 1 + rho, the between_two target (clamped to 0.1 to 0.9). With three or "
         "more candidates (two_choices) the achieved share should sit between the two, nearer the dotted "
         "line as the spread in rho grows.",
         [q(cand(achieved), "{{candidate}} achieved", "A"),
          q(cand(cap_share), "{{candidate}} capacity share", "B"),
          q(cand(eff_share), "{{candidate}} capacity / (1 + rho)", "C")],
         unit="percentunit", minv=0, maxv=1,
         overrides=[dashed("B", [10, 6]), dashed("C", [2, 4])])
    b.ts("Decisions by reason", "Every outcome the gateway records. Refusals carry an empty site.",
         [q(rate("grid_route_decisions_total", GW, "reason"), "{{reason}}")], unit="reqps", stack=True, w=12)
    arms = rate("grid_route_selections_total", 'cluster=~"$cluster"', "cluster, path")
    b.ts("Draw arm share",
         "Share of each gateway's decisions by the arm that made them, each arm its own line. "
         "two_choices: three or more candidates with room, the less saturated of two capacity-weighted "
         "draws; a stage where it is not the main arm does not exercise the selection logic. "
         "between_two: two with room, capacity over 1 + rho. lone: one with room, from backlog "
         "exclusion or a sticky prefix. by_capacity: none with room, or a candidate with no saturation "
         "reading.",
         [q(f"{arms} / on (cluster) group_left sum by (cluster) ({arms})", "{{cluster}} {{path}}")],
         unit="percentunit", minv=0, maxv=1, w=12)
    b.ts("Draw arm taken",
         "Decisions per second by arm, stacked: the volume behind the shares beside it.",
         [q(arms, "{{cluster}} {{path}}")],
         unit="reqps", stack=True, w=12)
    b.ts("Prefix affinity",
         "Prefix-affinity outcomes. sticky sends a prefix back to the candidate that served it, which "
         "narrows the draw to one (the lone arm). no_match: a prefix seen for the first time. "
         "not_applicable: a prompt shorter than one 256-byte block.",
         [q(rate("grid_route_prefix_affinity_total", 'cluster=~"$cluster"', "cluster, outcome"), "{{cluster}} {{outcome}}")],
         unit="reqps", stack=True, w=12)
    b.ts("Completions per pool", "vLLM completions per pool: the cross-check of the routed share.",
         [q(rate("vllm:request_success_total", POOL, "cluster, namespace"), "{{cluster}} / {{namespace}}")],
         unit="reqps", stack=True)
    b.end_row()

    # Row 2b: what came back.
    b.row("What came back, per candidate")
    ok = picked(count(RESP_OK, "cluster, backend"))
    done = picked(count(RESP, "cluster, backend"))
    decided = count(DEC, "cluster, backend")
    errors = picked(count(RESP_ERR, "cluster, backend"))
    zero = f"({done} * 0)"
    # vLLM records a client disconnect as finish reason abort. Pools join candidates by name, which
    # holds where each candidate is named for its pool namespace, as on this lab.
    aborted = ('sum by (candidate) (label_replace('
               + count('vllm:request_success_total{cluster=~"$cluster",finished_reason="abort"}', "namespace")
               + ', "candidate", "$1", "namespace", "(.*)"))')
    one = lambda expr: f"sum by (candidate) ({cand(expr)})"
    b.table("Per candidate over the selected range",
            "Counted exactly per gateway pod over the whole time range. Share is of decisions. Success "
            "is 2xx over finished responses, so requests still in flight count as neither. Errors are "
            "4xx and 5xx the gateway returned. Aborted counts requests the pool's vLLM ended because the "
            "client went away, which the gateway records no status for. Set the time range to the run "
            "window.",
            [("Decisions", one(decided), "none"),
             ("Share", one(f"{decided} / on (cluster) group_left sum by (cluster) ({decided})"), "percentunit"),
             ("Finished", one(done), "none"),
             ("Errors", one(f"{errors} or on (cluster, backend) {zero}"), "none"),
             ("Aborted", f"{aborted} or on (candidate) ({one(done)} * 0)", "none"),
             ("Success", one(f"{ok} / on (cluster, backend) ({done} > 0)"), "percentunit")])
    resp_by_pod = (f"sum by (cluster, pod) (praxis_http_requests_total{{job=\"grid-gateway-metrics\",cluster=~\"$cluster\"}} "
                   f"and on (cluster, backend) {PICKED})")
    b.stat("Requests unaccounted",
           "Per gateway pod, requests it decided minus those it finished minus those still in flight, "
           "summed. 0 means every routed request is accounted for. Above 0, requests ended without "
           "any response the gateway counted, such as a client that gave up. Since each pod started.",
           f"sum(sum by (cluster, pod) ({DEC}) - {resp_by_pod} "
           f"- sum by (cluster, pod) (praxis_http_active_requests{{listener=\"gateway\",cluster=~\"$cluster\"}}))",
           decimals=0, w=24,
           thresholds={"mode": "absolute", "steps": [{"color": "green", "value": None},
                                                    {"color": "orange", "value": 1},
                                                    {"color": "red", "value": 10}]})
    b.ts("Responses per candidate by status class",
         "What the gateway returned for requests it sent to each candidate. 5xx includes the gateway's "
         "own failures, such as a backend missing from its config. 4xx folds 401, 404 and 429 together.",
         [q(cand(picked(f"sum by (cluster, backend, status_class) (rate({RESP}[$__rate_interval]))")),
            "{{candidate}} {{status_class}}")], unit="reqps", stack=True)
    b.ts("Success ratio per candidate",
         "2xx over all finished responses, per candidate, per window. Requests in flight are not "
         "counted. Empty where a window finished none.",
         [q(cand(picked(f"sum by (cluster, backend) (rate({RESP_OK}[$__rate_interval])) "
                        f"/ on (cluster, backend) (sum by (cluster, backend) (rate({RESP}[$__rate_interval])) > 0)")),
            "{{candidate}}")], unit="percentunit", minv=0, maxv=1)
    b.ts("Gateway failures per candidate", "5xx responses per candidate. Any line above zero is a broken path.",
         [q(cand(picked(f"sum by (cluster, backend) (rate({RESP.replace('}', ',status_class=\"5xx\"}')}[$__rate_interval]))")),
            "{{candidate}}")], unit="reqps", novalue="no failures")
    b.end_row()

    # Row 3: what the decider read.
    b.row("What the decider read, per candidate")
    def per(metric, desc, unit="short", title=None, w=12, smooth=False):
        # smooth: average each window, since one 60 s hub sample can catch a single-request spike.
        sel = f"{metric}{{{GW}}}"
        inner = f"avg_over_time({sel}[$__rate_interval])" if smooth else sel
        b.ts(title or metric, desc,
             [q(cand(f'avg by (cluster, site, backend) ({inner})'), "{{site}} / {{candidate}}")],
             unit=unit, w=w, mappings=unmeasured())
    per("grid_route_site_rho",
        "Saturation the draw weighs: the nearest limit. Averaged over each window: at low concurrency a "
        "candidate holds one to three requests, so single samples jump between zero and a spike.",
        unit="percentunit", title="Saturation used (rho), window average", smooth=True)
    b.ts("Saturation reported and counted",
         "Solid: what the site reports (KV utilization plus pod spread). Dashed: in flight over ceiling, "
         "a reading only once a hard bound exists. rho follows whichever is nearer its limit." + LEARNED,
         [q(cand(f'avg by (cluster, site, backend) (avg_over_time(grid_route_site_rho_reported{{{GW}}}[$__rate_interval]))'),
            "{{candidate}} reported", "A"),
          q(cand(f'avg by (cluster, site, backend) (grid_route_site_rho_counted{{{GW}}})'),
            "{{candidate}} counted", "B")],
         unit="percentunit", overrides=[dashed("B", [10, 6])], mappings=unmeasured())
    per("grid_route_site_weight", "Capacity the draw weights by, after the explore floor." + LEARNED,
        title="Weight (capacity after explore floor)")
    b.ts("Ceilings",
         "Solid: the ceiling a count is read against. Dashed: soft, most carried with nothing waiting. "
         "Dotted: hard, fewest held at a limit." + LEARNED,
         [q(cand(f'avg by (cluster, site, backend) (grid_route_site_ceiling{{{GW}}})'), "{{candidate}} ceiling", "A"),
          q(cand(f'avg by (cluster, site, backend) (grid_route_site_ceiling_soft{{{GW}}})'), "{{candidate}} soft", "B"),
          q(cand(f'avg by (cluster, site, backend) (grid_route_site_ceiling_hard{{{GW}}})'), "{{candidate}} hard", "C")],
         overrides=[dashed("B", [10, 6]), dashed("C", [2, 4])], mappings=unmeasured())
    # The operator relays in-flight per provider whether or not the gateway learns; its provider name is
    # the gateway's backend name, so a rename joins them. Read from the deciding gateway's own operator.
    relayed = ('label_replace(max by (cluster, grid_provider) (grid_provider_in_flight_requests{cluster=~"$cluster"}) '
               'and on (cluster) group by (cluster) (grid_route_decisions_total), "backend", "$1", "grid_provider", "(.*)")')
    b.ts("In flight per candidate",
         "Solid: requests in flight at the candidate as the operator relays them, live today. Dashed: "
         "the same count as the gateway reads it for the decision. Dotted: the ceiling it is read "
         "against. The dashed and dotted lines are empty until the gateway learns capacity, which "
         "needs the operator to relay grid_provider_config_generation with its identity labels (grid "
         "#386 or later).",
         [q(cand(relayed), "{{candidate}} relayed", "A"),
          q(cand(f'avg by (cluster, site, backend) (grid_route_site_in_flight{{{GW}}})'), "{{candidate}} gateway", "B"),
          q(cand(f'avg by (cluster, site, backend) (grid_route_site_ceiling{{{GW}}})'), "{{candidate}} ceiling", "C")],
         overrides=[dashed("B", [10, 6]), dashed("C", [2, 4])], mappings=unmeasured())
    b.ts("Load over the ceiling, unclamped",
         "In flight over the ceiling it is read against, per candidate, without the clamp at 1 that "
         "counted saturation applies. Above 1 the candidate holds more than its ceiling: counted "
         "saturation reads 1.0 there whether it holds one request over or twice the ceiling, and "
         "routing excludes it either way." + LEARNED,
         [q(cand(f'avg by (cluster, site, backend) (grid_route_site_in_flight{{{GW}}}) / on (cluster, site, backend) '
                 f'avg by (cluster, site, backend) (grid_route_site_ceiling{{{GW}}})'), "{{site}} / {{candidate}}")],
         unit="percentunit", minv=0, mappings=unmeasured())
    b.ts("Clamped share of counted saturation",
         "Share of samples in each window where counted saturation sat at exactly 1.0. While it is high "
         "the decider cannot tell busy candidates apart: every one of them reads full." + LEARNED,
         [q(cand(f'avg by (cluster, site, backend) (avg_over_time((grid_route_site_rho_counted{{{GW}}} == bool 1)'
                 f'[$__rate_interval:]))'), "{{site}} / {{candidate}}")],
         unit="percentunit", minv=0, maxv=1, mappings=unmeasured())
    per("grid_route_site_score", "Window-worst EPP queue depth the candidate ranks by. Gap: excluded or demoted.",
        title="Queue score")
    per("grid_route_site_reading_age_seconds",
        "Age of the sample the decision read. A line that climbs is a frozen input, not a steady one."
        + LEARNED + " The age is lost on the fallback path too, which is a gateway gap.",
        unit="s", title="Reading age")
    b.state("Full", "1 while the candidate has a backlog at its ceiling long enough to shed." + LEARNED,
            [q(cand(f'max by (cluster, site, backend) (grid_route_site_full{{{GW}}})'), "{{candidate}}")],
            mappings=[{"type": "value", "options": {"0": {"text": "room", "color": "green"},
                                                   "1": {"text": "full", "color": "red"}}}]
            + unmeasured())
    b.end_row()

    # Row 4: can the inputs be trusted.
    b.row("Can the inputs be trusted")
    b.ts("Gateway polls of its operator", "Result of each poll of the local operator's signals.",
         [q(rate("grid_signals_poll_total", 'cluster=~"$cluster"', "cluster, result"), "{{cluster}} {{result}}")],
         unit="reqps", w=12)
    b.ts("Since last successful poll",
         "Seconds since any gateway in the cluster last read its operator. Newest across pods, so a "
         "replaced pod does not read as a frozen poll while its last sample lingers.",
         [q('time() - max by (cluster) (grid_signals_last_success_timestamp_seconds{cluster=~"$cluster"})',
            "{{cluster}}")], unit="s", w=12)
    b.ts("Signal rows refused and feedback failures",
         "Rows the gateway refused, by reason, and failed feedback deliveries to the operator, by reason.",
         [q(rate("grid_signals_ingest_dropped_total", 'cluster=~"$cluster"', "reason"), "dropped {{reason}}", "A"),
          q(rate("grid_signals_feedback_failures_total", 'cluster=~"$cluster"', "reason"), "feedback {{reason}}", "B")],
         unit="reqps", w=12)
    b.ts("Provider scrapes by result", "The operator's scrapes of each provider's EPP.",
         [q(rate("grid_provider_scrape_total", 'cluster=~"$cluster"', "cluster, grid_provider, result"),
            "{{cluster}} {{grid_provider}} {{result}}")], unit="reqps", w=12)
    b.ts("Since last provider scrape", "Seconds since the operator last scraped each provider.",
         [q('time() - max by (cluster, grid_provider) '
            '(grid_provider_last_scrape_success_timestamp_seconds{cluster=~"$cluster"})',
            "{{cluster}} {{grid_provider}}")], unit="s", w=12)
    b.ts("Provider ready endpoints", "Endpoints each provider reports ready, as the operator relays it.",
         [q('max by (cluster, grid_provider) (grid_provider_ready_endpoints{cluster=~"$cluster"})',
            "{{cluster}} {{grid_provider}}")], w=12)
    b.end_row()

    # Row 4b: signal coverage across sites.
    b.row("Signal coverage across sites")
    peer = 'cluster=~"$cluster"'
    b.ts("Peer polls by outcome",
         "Each operator's polls of its peer sites' signals. The cluster is the poller, the peer is the "
         "site polled.",
         [q(rate("grid_peer_poll_total", peer, "cluster, peer, outcome"), "{{cluster}} polls {{peer}}: {{outcome}}")],
         unit="reqps", stack=True)
    b.ts("Since last peer poll success",
         "Seconds since each operator last read each peer. On the hub this includes up to about 90 s of "
         "collection delay, so read trends here and exact values on a cluster's own querier.",
         [q(f"time() - max by (cluster, peer) (grid_peer_last_success_timestamp_seconds{{{peer}}})",
            "{{cluster}} from {{peer}}")], unit="s")
    b.ts("Peer poll duration p95", "How long one poll of a peer took.",
         [q(f"histogram_quantile(0.95, sum by (le, cluster, peer) "
            f"(rate(grid_peer_poll_duration_seconds_bucket{{{peer}}}[$__rate_interval])))",
            "{{cluster}} polls {{peer}}")], unit="s")
    b.ts("Peer observations refused by reason",
         "Rows from a peer the operator's ingest refused, by reason. reason=name is a series the peer "
         "exports and this ingest does not admit, which can be steady and harmless.",
         [q(rate("grid_peer_signals_refused_total", peer, "cluster, peer, reason"),
            "{{cluster}} from {{peer}}: {{reason}}")], unit="reqps", novalue="none refused")
    proposed = ("Proposed in the signal coverage issue draft and not exported yet, so this panel reads "
                "'not exported yet' until the operator publishes it.")
    b.ts("Signals relayed per provider (proposed)",
         "Solid: distinct series the operator relayed for each provider. Dashed: the median across its "
         "site's providers. A provider below its site median is missing inputs. " + proposed,
         [q('max by (cluster, grid_site, grid_provider) (grid_provider_signal_series{cluster=~"$cluster"})',
            "{{cluster}} {{grid_site}}/{{grid_provider}}", "A"),
          q('quantile by (cluster, grid_site) (0.5, grid_provider_signal_series{cluster=~"$cluster"})',
            "{{cluster}} {{grid_site}} median", "B")],
         overrides=[dashed("B", [10, 6])], novalue="not exported yet")
    b.ts("Missing routing inputs per provider (proposed)",
         "Signals the last relay did not carry for each provider, by name. Any line above 0 is a provider "
         "the gateway reads on fewer inputs than it should. " + proposed,
         [q('count by (cluster, grid_site, grid_provider, signal) (grid_provider_signal_present{cluster=~"$cluster"} == 0)',
            "{{cluster}} {{grid_provider}} missing {{signal}}")], novalue="not exported yet")
    b.ts("Peer observations accepted (proposed)",
         "Share of each peer's observations the operator's ingest accepted. A steady share below 100% "
         "is expected where a peer exports names this ingest does not admit. " + proposed,
         [q(f"{rate('grid_peer_signals_accepted_total', peer, 'cluster, peer')} / on (cluster, peer) "
            f"({rate('grid_peer_signals_accepted_total', peer, 'cluster, peer')} + on (cluster, peer) "
            f"{rate('grid_peer_signals_refused_total', peer, 'cluster, peer')})",
            "{{cluster}} from {{peer}}")], unit="percentunit", minv=0, maxv=1, novalue="not exported yet")
    b.ts("Signal age at the operator against at the gateway (proposed)",
         "Solid: age of each provider's newest sample as its operator relayed it (proposed). Dashed: "
         "the age the gateway read for the same candidate (exists today). The gap between them is "
         "the hop from operator to gateway; the solid line alone is scrape plus peer poll. " + proposed,
         [q(cand('label_replace(max by (cluster, grid_provider) (grid_provider_signal_age_seconds{cluster=~"$cluster"}), '
                 '"backend", "$1", "grid_provider", "(.*)")'), "{{candidate}} at operator", "A"),
          q(cand(f'avg by (cluster, site, backend) (grid_route_site_reading_age_seconds{{{GW}}})'),
            "{{candidate}} at gateway", "B")],
         unit="s", overrides=[dashed("B", [10, 6])], novalue="not exported yet")
    b.end_row()

    # Row 5: what it cost.
    b.row("What it cost, per pool")
    def quant(metric, qv):
        return (f"histogram_quantile({qv}, sum by (le, cluster, namespace) "
                f"(rate({metric}_bucket{{{POOL}}}[$__rate_interval])))")
    b.ts("Time to first token", "Solid p50, dashed p95, per pool.",
         [q(quant("vllm:time_to_first_token_seconds", 0.5), "{{namespace}} p50", "A"),
          q(quant("vllm:time_to_first_token_seconds", 0.95), "{{namespace}} p95", "B")],
         unit="s", w=12, overrides=[dashed("B", [10, 6])])
    b.ts("End-to-end latency", "Solid p50, dashed p95, per pool.",
         [q(quant("vllm:e2e_request_latency_seconds", 0.5), "{{namespace}} p50", "A"),
          q(quant("vllm:e2e_request_latency_seconds", 0.95), "{{namespace}} p95", "B")],
         unit="s", w=12, overrides=[dashed("B", [10, 6])])
    b.ts("Decode time p95", "Time spent generating after the first token, per pool.",
         [q(quant("vllm:request_decode_time_seconds", 0.95), "{{namespace}}")], unit="s", w=12)
    b.ts("Inter-token latency p95", "Per pool.",
         [q(quant("vllm:inter_token_latency_seconds", 0.95), "{{namespace}}")], unit="s", w=12)
    b.ts("Queue time mean", "Mean time a request waited in the pool before running.",
         [q(f"{rate('vllm:request_queue_time_seconds_sum', POOL, 'cluster, namespace')} / "
            f"{rate('vllm:request_queue_time_seconds_count', POOL, 'cluster, namespace')}", "{{namespace}}")],
         unit="s", w=12)
    b.ts("Running and waiting", "Solid running, dashed waiting.",
         [q(f'sum by (cluster, namespace) (vllm:num_requests_running{{{POOL}}})', "{{namespace}} running", "A"),
          q(f'sum by (cluster, namespace) (vllm:num_requests_waiting{{{POOL}}})', "{{namespace}} waiting", "B")],
         w=12, overrides=[dashed("B", [10, 6])])
    b.ts("KV cache usage", "Per pool.",
         [q(f'avg by (cluster, namespace) (vllm:kv_cache_usage_perc{{{POOL}}})', "{{namespace}}")],
         unit="percentunit", w=12, minv=0, maxv=1)
    b.ts("Preemptions and failed requests",
         "Solid: preemptions, the cost of admitting past a pool's limit. Dashed: aborted or failed requests.",
         [q(rate("vllm:num_preemptions_total", POOL, "cluster, namespace"), "{{namespace}} preempted", "A"),
          q(rate("vllm:request_success_total", POOL + ',finished_reason=~"abort|error"', "cluster, namespace"),
            "{{namespace}} failed", "B")],
         unit="reqps", w=12, overrides=[dashed("B", [10, 6])])
    gw_lat = (f"{rate('praxis_http_request_duration_seconds_sum', 'cluster=~\"$cluster\"', 'cluster, backend')} / "
              f"{rate('praxis_http_request_duration_seconds_count', 'cluster=~\"$cluster\"', 'cluster, backend')}")
    b.ts("Gateway mean latency per candidate",
         "The gateway's own request duration per candidate. A summary, so a mean, not a quantile.",
         [q(cand(picked(gw_lat)), "{{candidate}}")], unit="s", w=12)
    b.ts("Tokens per second", "Solid, left axis: generated. Dashed, right axis: prompt tokens read. Per pool.",
         [q(rate("vllm:generation_tokens_total", POOL, "cluster, namespace"), "{{namespace}} generated", "A"),
          q(rate("vllm:prompt_tokens_total", POOL, "cluster, namespace"), "{{namespace}} prompt", "B")],
         w=12, overrides=[right("B", [10, 6])])
    b.ts("Gateway responses by status class",
         "Every response the grid gateways returned. 4xx collapses 401, 404 and 429 into one class.",
         [q(rate("praxis_http_requests_total", 'cluster=~"$cluster",job="grid-gateway-metrics"',
                 "cluster, status_class"), "{{cluster}} {{status_class}}")],
         unit="reqps", w=12, stack=True)
    b.end_row()

    # Row 6: inside each pool.
    b.row("Inside each pool (EPP)")
    def epp(avg, sd, title, unit="short"):
        b.ts(title, "Solid: the pool average. Dashed: the spread across its endpoints, which stays 0 "
             "while a pool runs one endpoint.",
             [q(f'max by (cluster, namespace) ({avg}{{{POOL}}})', "{{namespace}} average", "A"),
              q(f'max by (cluster, namespace) ({sd}{{{POOL}}})', "{{namespace}} spread", "B")],
             unit=unit, w=12, overrides=[dashed("B", [10, 6])],
             minv=0, maxv=1 if unit == "percentunit" else None)
    epp("llm_d_epp_average_queue_size", "llm_d_epp_std_dev_queue_size", "Queue")
    epp("llm_d_epp_average_kv_cache_utilization", "llm_d_epp_std_dev_kv_cache_utilization", "KV cache", "percentunit")
    epp("llm_d_epp_average_running_requests", "llm_d_epp_std_dev_running_requests", "Running")
    b.ts("Ready endpoints and EPP poll errors",
         "Solid, left axis: endpoints whose last EPP scrape is fresh, not ready pods. Dashed, right "
         "axis: the EPP's failed scrapes of its endpoints in each window.",
         [q(f'max by (cluster, namespace) (llm_d_epp_ready_endpoints{{{POOL}}})', "{{namespace}} ready", "A"),
          q(f"sum by (cluster, namespace) (increase(llm_d_epp_datalayer_poll_errors_total{{{POOL}}}[$__rate_interval]))",
            "{{namespace}} poll errors", "B")],
         w=12, overrides=[right("B", [10, 6])])
    b.ts("Prefix cache hit ratio", "Prompt tokens served from the prefix cache, per pool.",
         [q(f"{rate('vllm:prefix_cache_hits_total', POOL, 'cluster, namespace')} / "
            f"clamp_min({rate('vllm:prefix_cache_queries_total', POOL, 'cluster, namespace')}, 1e-9)",
            "{{namespace}}")], unit="percentunit", w=12, minv=0, maxv=1)
    hits = rate("vllm:prefix_cache_hits_total", POOL, "cluster, namespace")
    queried = rate("vllm:prefix_cache_queries_total", POOL, "cluster, namespace")
    b.ts("Prefix cache hits and misses",
         "Prompt tokens per second per pool. Solid, right axis: served from the prefix cache. Dashed, "
         "left axis: missed, queried minus hit, which the engine computed afresh. Separate axes, since "
         "misses run hundreds of times higher. Unique prompts read all miss.",
         [q(hits, "{{namespace}} hit", "A"),
          q(f"{queried} - {hits}", "{{namespace}} miss", "B")],
         w=12, overrides=[right("A"), dashed("B", [10, 6])])
    b.ts("EPP prefix index size",
         "Entries in each pool's EPP prefix index. The EPP indexes only requests it schedules, so this "
         "stays 0 while the gateway sends straight to each pool's vLLM Service and skips the EPP.",
         [q(f'max by (cluster, namespace) (llm_d_epp_prefix_indexer_size{{{POOL}}})', "{{namespace}}")], w=12)
    b.end_row()

    # Row 7: GPUs.
    b.row("GPUs")
    gpu_legend = "{{cluster}} / {{exported_namespace}} gpu{{gpu}} {{modelName}}"
    b.ts("GPU busy per pool",
         "Mean utilization of the GPUs each pool holds, from DCGM. A pool split across GPUs (TP2) "
         "averages them. Utilization is the share of time a kernel runs, not how hard it works.",
         [q('avg by (cluster, exported_namespace) (DCGM_FI_DEV_GPU_UTIL{cluster=~"$cluster",exported_namespace!=""})',
            "{{cluster}} / {{exported_namespace}}")], unit="percent", minv=0, maxv=100)
    b.ts("GPU utilization", "Per GPU, labelled with the pool holding it. An unlabelled GPU is idle.",
         [q('avg by (cluster, exported_namespace, gpu, modelName) (DCGM_FI_DEV_GPU_UTIL{cluster=~"$cluster"})',
            gpu_legend)], unit="percent", minv=0, maxv=100)
    b.ts("GPU framebuffer used", "Per GPU, labelled with the pool holding it.",
         [q('max by (cluster, exported_namespace, gpu, modelName) (DCGM_FI_DEV_FB_USED{cluster=~"$cluster"})',
            gpu_legend)], unit="decmbytes")
    b.end_row()

    def var(name, label, query, multi=True):
        return {"name": name, "type": "query", "label": label, "datasource": DS,
                "query": {"query": query, "refId": name}, "definition": query,
                "multi": multi, "includeAll": True, "allValue": ".*", "refresh": 2, "sort": 1,
                "current": {"selected": True, "text": ["All"], "value": ["$__all"]}}

    new_pod = ('count by (cluster, pod) ({sel}) unless count by (cluster, pod) ({sel} offset 2m)')
    annotations = [
        {"name": "Serving config reload", "datasource": DS, "enable": True, "iconColor": "blue",
         "expr": 'sum by (cluster, result) (increase(grid_serving_config_reload_total{cluster=~"$cluster"}[2m])) > 0',
         "titleFormat": "serving config reload", "textFormat": "{{cluster}} {{result}}", "step": "60s"},
        {"name": "Gateway pod started", "datasource": DS, "enable": True, "iconColor": "orange",
         "expr": new_pod.format(sel='up{job="grid-gateway-metrics",cluster=~"$cluster"}'),
         "titleFormat": "gateway pod started", "textFormat": "{{cluster}} {{pod}}", "step": "60s"},
        {"name": "EPP pod started", "datasource": DS, "enable": True, "iconColor": "purple",
         "expr": new_pod.format(sel='llm_d_epp_info{cluster=~"$cluster"}'),
         "titleFormat": "EPP pod started", "textFormat": "{{cluster}} {{pod}}", "step": "60s"},
    ]

    return {
        "title": "AI Grid routing tuning", "uid": "ai-grid-routing-tuning", "schemaVersion": 39,
        "version": 1, "editable": True, "tags": ["ai-grid", "routing"], "timezone": "browser",
        "refresh": "1m", "time": {"from": "now-3h", "to": "now"},
        "description": "Where requests went, what the decider read, whether to trust it, and what it cost. "
                       "Hub samples arrive every 60 s; point the datasource at a cluster's own querier "
                       "for the fine view.",
        "annotations": {"list": annotations},
        "templating": {"list": [
            {"name": "datasource", "type": "datasource", "query": "prometheus", "label": "Datasource",
             "current": {}, "hide": 0},
            var("cluster", "Cluster", "label_values(up, cluster)"),
            var("site", "Grid site", "label_values(grid_route_site_weight, site)"),
            var("pool", "Pool", "label_values(vllm:num_requests_running, namespace)"),
        ]},
        "panels": b.panels,
    }


def main():
    board = build()
    body = json.dumps(board, indent=2)
    sys.stdout.write(
        "# Generated by ../dashboards/routing_tuning.py; edit the script, not this file.\n"
        "apiVersion: v1\n"
        "kind: ConfigMap\n"
        "metadata:\n"
        "  name: grafana-dashboard-ai-grid-routing-tuning\n"
        "  namespace: open-cluster-management-observability\n"
        "  labels:\n"
        '    grafana-custom-dashboard: "true"\n'
        "  annotations:\n"
        "    observability.open-cluster-management.io/dashboard-folder: AI Grid\n"
        '    argocd.argoproj.io/sync-wave: "5"\n'
        "data:\n"
        "  ai-grid-routing-tuning.json: |\n")
    for line in body.splitlines():
        sys.stdout.write(f"    {line}\n")


if __name__ == "__main__":
    main()
