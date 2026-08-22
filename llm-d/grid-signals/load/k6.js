// Offer steady load to one site's consumer gateway.
//
// Constant arrival rate rather than a fixed number of virtual users: the point
// is to hold an offered rate while the pool's own latency moves, and VUs would
// throttle themselves as soon as the pool slowed down, which is the moment the
// measurement matters.
import http from 'k6/http';

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 40),
      timeUnit: '1s',
      duration: __ENV.DURATION || '120s',
      preAllocatedVUs: 50,
      maxVUs: 300,
    },
  },
  // The pool is meant to saturate. A failed request is data, not a reason to
  // abort the run.
  thresholds: {},
};

const body = JSON.stringify({
  model: __ENV.MODEL || 'Qwen/Qwen3-0.6B',
  messages: [{ role: 'user', content: 'Explain consensus, replication and partitioning in detail.' }],
  max_tokens: 64,
});

export default function () {
  http.post(`${__ENV.TARGET}/v1/chat/completions`, body, {
    headers: { 'Content-Type': 'application/json' },
    timeout: '30s',
  });
}
