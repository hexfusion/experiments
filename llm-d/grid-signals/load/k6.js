// Offer load and record which pool answered.
//
// Arrival rate, not a fixed VU pool: users throttle themselves the moment a
// pool slows, which is when the measurement matters.
//
// The provider gateway stamps X-Grid-LlmD-Provider-Gateway on each response.
// It is the only per-request record of a routing decision; pool metrics show
// what a pool is doing, not what the router chose.
import http from 'k6/http';
import { Counter } from 'k6/metrics';

const served = new Counter('served_by_pool');

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: Number(__ENV.START_RATE || 2),
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 400,
      stages: [
        { target: Number(__ENV.PEAK_RATE || 24), duration: __ENV.RAMP || '150s' },
        { target: Number(__ENV.PEAK_RATE || 24), duration: __ENV.HOLD || '180s' },
      ],
    },
  },
  // The pool is meant to fall behind. A failed request is data, not a reason
  // to abort the run.
  thresholds: {},
};

const body = JSON.stringify({
  model: __ENV.MODEL || 'Qwen/Qwen3-0.6B',
  messages: [{ role: 'user', content: 'Explain consensus, replication and partitioning in detail.' }],
  max_tokens: 64,
});

// Affinity binds a session to a cluster, so unique ids per request measure
// nothing and a single id measures one binding.
const SESSIONS = Number(__ENV.SESSIONS || 0);

export default function () {
  const headers = { 'Content-Type': 'application/json' };
  if (SESSIONS > 0) {
    headers['X-Session-Id'] = `s-${Math.floor(Math.random() * SESSIONS)}`;
  }
  const res = http.post(`${__ENV.TARGET}/v1/chat/completions`, body, {
    headers,
    timeout: '30s',
  });
  const pool = res.headers['X-Grid-Llmd-Provider-Gateway'] || 'unattributed';
  served.add(1, { pool });
}
