"""Plot one demo run: per-endpoint routed share and Portland in flight, beats shaded.
usage: plot_run.py <start HH:MM:SS> <end HH:MM:SS> <T1 HH:MM:SS> <T2 HH:MM:SS> <out.png> [streams]"""
import sys, json, subprocess, datetime, urllib.request, urllib.parse, ssl
import matplotlib; matplotlib.use('Agg'); import matplotlib.pyplot as plt
start, end, t1, t2, out = sys.argv[1:6]; streams = sys.argv[6] if len(sys.argv) > 6 else '24'
tok = subprocess.check_output(['oc', '--kubeconfig', '/home/sbatsche/.kube/grid.kubeconfig', '--context', 'dagobah', 'whoami', '-t'], text=True).strip()
Q = 'https://thanos-querier-openshift-monitoring.apps.dagobah.hexfusion.local/api/v1/query_range'
ctx = ssl.create_default_context(); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
ts = lambda s: int(datetime.datetime.strptime(s, '%Y-%m-%d %H:%M:%S').replace(tzinfo=datetime.UTC).timestamp())
def rq(expr, step=10):
    data = urllib.parse.urlencode({'query': expr, 'start': ts(start), 'end': ts(end), 'step': step}).encode()
    req = urllib.request.Request(Q, data=data, headers={'Authorization': f'Bearer {tok}'})
    return json.load(urllib.request.urlopen(req, context=ctx))['data']['result']
share = rq('sum by (backend) (rate(grid_route_decisions_total{reason="routed"}[1m])) / ignoring(backend) group_left sum(rate(grid_route_decisions_total{reason="routed"}[1m]))')
infl = rq('sum by (grid_provider) (grid_provider_in_flight_requests)', 5)
CITY = {'hq-east': 'Seattle east', 'hq-west': 'Seattle west', 'factory': 'Portland', 'retail': 'San Jose'}
COL = {'hq-east': '#b9770e', 'hq-west': '#2d6a9f', 'factory': '#2d6a4f', 'retail': '#a4262c'}
t0 = ts(start)
fig, (a1, a2) = plt.subplots(2, 1, figsize=(11, 5.2), sharex=True, gridspec_kw={'height_ratios': [1, 1.4]})
for s in infl:
    k = s['metric']['grid_provider']
    if k not in ('factory', 'hq-east', 'hq-west'): continue
    xs = [(float(t) - t0) / 60 for t, _ in s['values']]; ys = [float(v) if v != 'NaN' else float('nan') for _, v in s['values']]
    a1.plot(xs, ys, color=COL[k], lw=2 if k == 'factory' else 1.2, label=CITY[k])
a1.set_ylabel('in flight'); a1.legend(loc='upper right', frameon=False, ncol=3, fontsize=9); a1.grid(alpha=.25)
for s in share:
    k = s['metric']['backend']
    if k == 'retail': continue
    xs = [(float(t) - t0) / 60 for t, _ in s['values']]; ys = [100 * float(v) if v != 'NaN' else float('nan') for _, v in s['values']]
    a2.plot(xs, ys, color=COL[k], lw=2 if k == 'factory' else 1.2, label=CITY[k])
a2.set_ylabel('share of routed requests, %'); a2.set_xlabel('minutes'); a2.set_ylim(0, 100); a2.grid(alpha=.25); a2.legend(loc='upper right', frameon=False, ncol=3, fontsize=9)
for ax in (a1, a2):
    ax.axvspan((ts(t1) - t0) / 60, (ts(t2) - t0) / 60, color='#2d6a4f', alpha=.08)
a2.text((ts(t1) - t0) / 60 + .05, 92, f'{streams} local streams at Portland', fontsize=9, color='#2d6a4f')
a2.text((ts(t2) - t0) / 60 + .05, 92, 'load stops', fontsize=9, color='#555')
for ax in (a1, a2):
    for sp in ('top', 'right'): ax.spines[sp].set_visible(False)
plt.tight_layout(); plt.savefig(out, dpi=160); print('wrote', out)
