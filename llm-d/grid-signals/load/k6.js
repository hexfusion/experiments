// Climb load against one site's consumer gateway.
//
// An arrival-rate executor rather than a fixed pool of virtual users, because
// users throttle themselves the moment the pool slows down, which is exactly
// when the measurement matters.
//
// Ramping rather than flat. A flat rate either sits under capacity and shows
// nothing or sits over it and saturates into a sawtooth, and neither makes the
// distance between what a pool measures and what its peers hold legible. A
// steady climb gives a rising line for the held view to trail.
import http from 'k6/http';

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

export default function () {
  http.post(`${__ENV.TARGET}/v1/chat/completions`, body, {
    headers: { 'Content-Type': 'application/json' },
    timeout: '30s',
  });
}
