// overflow-ramp.js — Arm 1 concurrency ramp for extproc-overflow-bench.
//
// Each VU holds ONE streaming (SSE) chat-completion open for its whole response,
// so active-VUs == concurrent held-open requests == concurrent ext_proc streams
// (in processing-mode A). Ramps concurrency through the breaker default (1024)
// and past it (2000, 4000) while the Envoy /stats scraper records overflow.
//
// The A/B is set on the GATEWAY/EPP side (response_body_mode SEND vs NONE), not
// here — this script is identical for both arms. Pair every run with
// scripts/scrape-envoy-stats.sh so 500s can be correlated to *_overflow / *_open.
//
// Usage:
//   k6 run -e BASE_URL=http://gw.host -e MODEL=qwen-7b-awq overflow-ramp.js
// Env:
//   BASE_URL   (required)  gateway ingress, e.g. http://10.0.0.x:80
//   MODEL      default qwen-7b-awq
//   PATH_      default /v1/chat/completions
//   MAX_TOKENS default 2000   (high → long hold → VU stays "concurrent")
//   PROMPT_REPEAT default 1    (inflate prompt size for a body-cost variant)
//   REQ_TIMEOUT default 600s
//   STAGES     default "1000,2000,4000,8000,16000,32000,64000" (target VUs per step)
//   STEP_RAMP  default 30s     (time to reach each step)
//   STEP_HOLD  default 45s     (time held at each step)
//   ABORT      default "0"     ("1" = abort the run once success collapses, so the
//                               last sustained step ≈ the ceiling; see threshold below)
//   ABORT_RATE default "0.95"  (success-rate floor for ABORT)
//   TAG        default ""      (label written into the summary filename)
//
// GOAL: push to the maximum concurrency the SYSTEM allows, then identify what
// binds. The load generator must NOT be the artificial ceiling — before a max
// run:
//   * raise client fds:  ulimit -n 1048576   (k6 holds ~1 fd per VU/conn)
//   * one source IP gives ~28k ephemeral ports — beyond that, run k6 IN-CLUSTER
//     as several pods (k6-operator or N replicas sharding STAGES) so you're not
//     NAT-bound from a laptop. Record k6's own CPU/mem each run.
//   * raise Envoy worker count (--concurrency) and gateway pod CPU/mem so the
//     proxy, not the box, is what you're measuring.
// Pair every max run with scripts/scrape-envoy-stats.sh AND
// scripts/scrape-pod-resources.sh so the binding resource at the knee is named
// (breaker overflow vs Envoy CPU saturation vs FD/mem vs sim backend).
// Export the time series:  k6 run --out csv=results/k6-ts-<tag>.csv ...

import http from 'k6/http';
import { Counter, Rate, Trend } from 'k6/metrics';
import exec from 'k6/execution';

const BASE_URL = __ENV.BASE_URL;
const MODEL = __ENV.MODEL || 'qwen-7b-awq';
const PATH_ = __ENV.PATH_ || '/v1/chat/completions';
const MAX_TOKENS = parseInt(__ENV.MAX_TOKENS || '2000', 10);
const PROMPT_REPEAT = parseInt(__ENV.PROMPT_REPEAT || '1', 10);
const REQ_TIMEOUT = __ENV.REQ_TIMEOUT || '600s';
const TAG = __ENV.TAG || '';

const STAGE_TARGETS = (__ENV.STAGES || '100,500,1024,2000,4000')
  .split(',').map((s) => parseInt(s.trim(), 10));
const STEP_RAMP = __ENV.STEP_RAMP || '30s';
const STEP_HOLD = __ENV.STEP_HOLD || '60s';

if (!BASE_URL) { throw new Error('BASE_URL is required'); }

// Build ramp→hold stages for each concurrency target.
const stages = [];
for (const t of STAGE_TARGETS) {
  stages.push({ duration: STEP_RAMP, target: t });
  stages.push({ duration: STEP_HOLD, target: t });
}
stages.push({ duration: '20s', target: 0 });

const maxVUs = Math.max(...STAGE_TARGETS);

// Metrics. The decisive one is rq_500 / overflow_500 — non-2xx under load.
const ttft = new Trend('ttft_ms', true);          // time-to-first-byte ≈ TTFT
const reqs = new Counter('reqs_total');
const non2xx = new Counter('reqs_non2xx');
const rq500 = new Counter('reqs_500');
const overflow500 = new Counter('reqs_overflow_500'); // 500 whose body smells of ext_proc overflow
const okRate = new Rate('req_success');
const vusActive = new Trend('vus_active');          // sampled concurrency for correlation

export const options = {
  insecureSkipTLSVerify: true,  // self-signed gateway cert
  discardResponseBodies: false, // we read first chunk for TTFT + overflow sniff
  scenarios: {
    ramp: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages,
      gracefulRampDown: '10s',
      gracefulStop: '30s',
    },
  },
  // No hard thresholds — we WANT to observe failures, not abort on them.
  thresholds: {},
};

const basePrompt =
  'Summarize the following text in detail and continue at length. ';
const prompt = basePrompt.repeat(PROMPT_REPEAT);

export default function () {
  vusActive.add(exec.instance.vusActive);

  const payload = JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: prompt }],
    max_tokens: MAX_TOKENS,
    stream: true,
  });

  const res = http.post(`${BASE_URL}${PATH_}`, payload, {
    headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
    timeout: REQ_TIMEOUT,
    tags: { name: 'chat_stream' },
  });

  reqs.add(1);
  ttft.add(res.timings.waiting); // TTFB on a streamed body ≈ time to first SSE event
  const ok = res.status >= 200 && res.status < 300;
  okRate.add(ok);

  if (!ok) {
    non2xx.add(1);
    if (res.status === 500) {
      rq500.add(1);
      const body = (res.body || '').toString();
      // Envoy emits "overflow" in the ext_proc local-reply body on breaker trip.
      if (body.includes('overflow') || body.includes('ext_proc')) {
        overflow500.add(1);
      }
    }
  }
}

export function handleSummary(data) {
  const suffix = TAG ? `-${TAG}` : '';
  const file = `results/k6-summary${suffix}.json`;
  const out = {};
  out[file] = JSON.stringify(data, null, 2);
  // also dump a flat one-liner to stdout for quick eyeballing
  const m = data.metrics;
  const g = (k) => (m[k] && (m[k].values.count ?? m[k].values.rate ?? m[k].values['p(95)'])) ?? 0;
  out.stdout =
    `\n=== overflow-ramp${suffix} ===\n` +
    `reqs=${g('reqs_total')} non2xx=${g('reqs_non2xx')} ` +
    `500=${g('reqs_500')} overflow500=${g('reqs_overflow_500')} ` +
    `success_rate=${(g('req_success') * 100).toFixed(2)}% ` +
    `ttft_p95=${(m['ttft_ms']?.values?.['p(95)'] ?? 0).toFixed(0)}ms\n`;
  return out;
}
