// k6 open-loop load test for the spoke-and-hub router. Unlike guidellm, k6 tags each request by the
// x-cluster response header, so we get PER-CLUSTER latency + distribution + errors -- the multi-cluster
// signal. constant-arrival-rate = open-loop (a slow/loaded cluster does NOT throttle offered load, so the
// load-shed benefit actually surfaces). No tokenizer / no HF -> runs offline in kind.
import http from 'k6/http';
import { Trend, Counter } from 'k6/metrics';

const TARGET = __ENV.TARGET;                       // http://<gateway>:8080
const MODE   = __ENV.MODE || 'load';               // load = open-loop per-cluster; affinity = per-VU sessions
const RATE   = parseInt(__ENV.RATE || '12');       // requests/sec (open-loop arrival rate, load mode)
const DURATION = __ENV.DURATION || '20s';
const MODEL  = __ENV.MODEL || 'model-a';
const OUT    = parseInt(__ENV.OUTPUT_TOKENS || '64');
const MAXVUS = parseInt(__ENV.MAXVUS || '400');    // headroom so arrivals hold even when responses slow
const VUS    = parseInt(__ENV.VUS || '8');         // affinity mode: 1 VU = 1 session

const latOverall = new Trend('lat_overall', true);
const latA = new Trend('lat_cluster_a', true);
const latB = new Trend('lat_cluster_b', true);
const latC = new Trend('lat_cluster_c', true);
const cntA = new Counter('reqs_cluster_a');
const cntB = new Counter('reqs_cluster_b');
const cntC = new Counter('reqs_cluster_c');
const cntU = new Counter('reqs_unknown');
const errs = new Counter('errors');
const stuck = new Counter('stuck');   // affinity: requests on the VU's first (pinned) cluster
const seen = new Counter('seen');     // affinity: total requests in a session VU

// per-VU session state (k6 instantiates the module per VU, so these are per-session)
let sessionToken = null;
let firstCluster = null;

export const options = {
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  scenarios: MODE === 'affinity'
    ? { affinity: { executor: 'constant-vus', vus: VUS, duration: DURATION } }   // 1 VU = 1 sticky session
    : { load: { executor: 'constant-arrival-rate', rate: RATE, timeUnit: '1s', duration: DURATION,
                preAllocatedVUs: Math.min(MAXVUS, Math.max(10, RATE * 4)), maxVUs: MAXVUS } },
};

const body = JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'The capital of France is' }], max_tokens: OUT });
const params = { headers: { 'content-type': 'application/json' }, timeout: '60s' };

export default function () {
  const h = Object.assign({}, params.headers);
  if (MODE === 'affinity' && sessionToken) h['x-session-token'] = sessionToken;   // replay -> stick
  const res = http.post(`${TARGET}/v1/chat/completions`, body, { headers: h, timeout: '60s' });
  const c = (res.headers['X-Cluster'] || res.headers['x-cluster'] || '?');
  if (res.status !== 200) { errs.add(1); return; }
  latOverall.add(res.timings.duration);
  if (c === 'cluster-a') { latA.add(res.timings.duration); cntA.add(1); }
  else if (c === 'cluster-b') { latB.add(res.timings.duration); cntB.add(1); }
  else if (c === 'cluster-c') { latC.add(res.timings.duration); cntC.add(1); }
  else cntU.add(1);

  if (MODE === 'affinity') {
    const tok = res.headers['X-Session-Token'] || res.headers['x-session-token'];
    if (tok) sessionToken = tok;                       // capture server-issued token, echo next time
    if (firstCluster === null) firstCluster = c;       // the cluster this session got pinned to
    seen.add(1);
    if (c === firstCluster) stuck.add(1);
  }
}

export function handleSummary(data) {
  const m = data.metrics;
  const g = (name, k) => (m[name] && m[name].values && m[name].values[k] != null) ? m[name].values[k] : null;
  const out = {
    rate: RATE, duration: DURATION,
    overall:   { p50: g('lat_overall', 'med'), p90: g('lat_overall', 'p(90)'), p99: g('lat_overall', 'p(99)'), avg: g('lat_overall', 'avg') },
    cluster_a: { p50: g('lat_cluster_a', 'med'), p90: g('lat_cluster_a', 'p(90)'), p99: g('lat_cluster_a', 'p(99)'), n: g('reqs_cluster_a', 'count') || 0 },
    cluster_b: { p50: g('lat_cluster_b', 'med'), p90: g('lat_cluster_b', 'p(90)'), p99: g('lat_cluster_b', 'p(99)'), n: g('reqs_cluster_b', 'count') || 0 },
    cluster_c: { p50: g('lat_cluster_c', 'med'), p90: g('lat_cluster_c', 'p(90)'), p99: g('lat_cluster_c', 'p(99)'), n: g('reqs_cluster_c', 'count') || 0 },
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
