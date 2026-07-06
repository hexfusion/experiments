import http from 'k6/http';
import { Trend, Counter } from 'k6/metrics';
import encoding from 'k6/encoding';

const TARGET   = __ENV.TARGET;
const MODE     = __ENV.MODE || 'load';
const RATE     = parseInt(__ENV.RATE || '12');
const DURATION = __ENV.DURATION || '20s';
const MODEL    = __ENV.MODEL || 'model-a';
const OUT      = parseInt(__ENV.OUTPUT_TOKENS || '64');
const MAXVUS   = parseInt(__ENV.MAXVUS || '400');
const VUS      = parseInt(__ENV.VUS || '8');
const CLUSTERS = (__ENV.CLUSTERS || 'cluster-a,cluster-b,cluster-c').split(',');

const latOverall = new Trend('lat_overall', true);
const lat = {}, cnt = {};
for (const c of CLUSTERS) { lat[c] = new Trend(`lat_${c}`, true); cnt[c] = new Counter(`reqs_${c}`); }
const cntU  = new Counter('reqs_unknown');
const errs  = new Counter('errors');
const stuck = new Counter('stuck');
const seen  = new Counter('seen');

let sessionToken = null;
let firstCluster = null;

export const options = {
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  scenarios: MODE === 'affinity'
    ? { affinity: { executor: 'constant-vus', vus: VUS, duration: DURATION } }
    : { load: { executor: 'constant-arrival-rate', rate: RATE, timeUnit: '1s', duration: DURATION,
                preAllocatedVUs: Math.min(MAXVUS, Math.max(10, RATE * 4)), maxVUs: MAXVUS } },
};

const body = JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'The capital of France is' }], max_tokens: OUT });
const params = { headers: { 'content-type': 'application/json' }, timeout: '60s' };

export default function () {
  const h = Object.assign({}, params.headers);
  if (MODE === 'affinity' && sessionToken) h['x-session-token'] = sessionToken;
  const res = http.post(`${TARGET}/v1/chat/completions`, body, { headers: h, timeout: '60s' });

  // x-session-token = base64(picked cluster NamespacedName), e.g. base64("default/ifc2")
  const tok = res.headers['X-Session-Token'] || res.headers['x-session-token'];
  let c = '?';
  if (tok) { const nn = encoding.b64decode(tok, 'std', 's'); c = nn.substring(nn.lastIndexOf('/') + 1); }
  else if (res.headers['X-Cluster']) { c = res.headers['X-Cluster']; }

  if (res.status !== 200) { errs.add(1); return; }
  latOverall.add(res.timings.duration);
  if (cnt[c]) { lat[c].add(res.timings.duration); cnt[c].add(1); } else cntU.add(1);

  if (MODE === 'affinity') {
    if (tok) sessionToken = tok;
    if (firstCluster === null) firstCluster = c;
    seen.add(1);
    if (c === firstCluster) stuck.add(1);
  }
}

export function handleSummary(data) {
  const m = data.metrics;
  const g = (name, k) => (m[name] && m[name].values && m[name].values[k] != null) ? m[name].values[k] : null;
  const clusters = {};
  for (const c of CLUSTERS) {
    clusters[c] = { p50: g(`lat_${c}`, 'med'), p90: g(`lat_${c}`, 'p(90)'), p99: g(`lat_${c}`, 'p(99)'), n: g(`reqs_${c}`, 'count') || 0 };
  }
  const out = {
    rate: RATE, duration: DURATION,
    overall: { p50: g('lat_overall', 'med'), p90: g('lat_overall', 'p(90)'), p99: g('lat_overall', 'p(99)'), avg: g('lat_overall', 'avg') },
    clusters,
    unknown: g('reqs_unknown', 'count') || 0,
    errors:  g('errors', 'count') || 0,
  };
  if (MODE === 'affinity') {
    const s = g('stuck', 'count') || 0, t = g('seen', 'count') || 0;
    out.mode = 'affinity';
    out.stickiness_pct = t ? Math.round(100 * s / t) : null;
  }
  return { stdout: 'K6SUMMARY ' + JSON.stringify(out) + '\n' };
}
