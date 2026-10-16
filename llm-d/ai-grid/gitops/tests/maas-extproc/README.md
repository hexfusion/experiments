# Isolate ExternalModel body-mode compatibility with MaaS

Historical diagnostic: this reproducer temporarily changes the test route's
Envoy mode. It is not the acceptance test for the current unchanged-Envoy rollout.
The [built-image test](../serving-payload/praxis/reproduce.py) and its
[result](../../evidence/2026-10-16/serving-payload/praxis/streaming-result.json) cover the standalone
Praxis compatibility change; full live integration remains pending.

This test uses an existing MaaS/Praxis tenant and gateway. It creates a new TLS
Katan mock, ExternalProvider, ExternalModel, MaaSModelRef, MaaSAuthPolicy,
MaaSSubscription, service account, and random provider credential. It makes real
MaaS API-key and inference calls. Grid and a GPU model are not involved.

Prerequisites: Python 3 with PyYAML, kubectl, an OpenShift service CA, and a healthy
MaaS tenant configured for Praxis. The gateway must already mount that service CA
at `tests.maasExtproc.providerCAPath`. Gateway authorization must be functioning;
an unrelated auth timeout must not be mistaken for the protocol failure.
Cluster-specific inputs, including a digest-pinned mock image, live in
`tests/config/dagobah.yaml`. The test does not change images or shared global filter modes.

From the experiments repository root:

```bash
python3 llm-d/ai-grid/gitops/tests/maas-extproc/reproduce.py \
  --values llm-d/ai-grid/gitops/sites/dagobah/site.yaml \
  --output /path/to/new-result-directory \
  --expect bug
```

The output directory must not exist. The test prints progress while waiting for
controllers and records sanitized evidence in `result.json`. API keys and
service-account tokens stay in memory. The mock's provider key is supplied from
a temporary Kubernetes Secret, never committed or printed.

| Case | Route body mode | Expected on affected gateway |
|---|---|---|
| Baseline, controller-generated route | STREAMED | 500 |
| Temporary test-model-only control | NONE | 200 with OpenAI completion |
| Control removed, baseline restored | STREAMED | 500 |

Each case runs both a normal completion and a streaming completion. Streaming
requests use chunked HTTP uploads. Successful SSE responses must contain content,
use `text/event-stream`, and terminate with `[DONE]`; event arrival times are
recorded. This does not yet test slow clients, cancellation, or quota accounting.

The test waits for the mode to appear in Envoy's actual config dump before each
request and checks Praxis logs for the matching protocol error. It does not
modify the controller-generated objects. Its priority-30 control EnvoyFilter
matches only the four routes of its unique test model. The control is diagnostic,
not a proposed production deployment pattern.

Use `--expect fixed` to require 200 in all three cases after selecting a gateway
with correct per-route protocol reporting. This is a regression gate for the
selected MaaS/controller/Praxis/gateway combination, not full MaaS qualification.

Normal completion and Python exceptions revoke the API key and delete explicitly
recorded run-owned resources. No shared namespace is deleted. After an uncatchable
process termination, inspect the run label `experiments.hexfusion.io/run` and
remove that run's resources; its generated `<run>-tls` Secret also needs removal.
API keys have a 15-minute expiry as a fallback. Do not infer full cleanup merely
from API-key expiry. A recorded cleanup error causes a failing exit status.

The [integration record](../../docs/maas-grid-integration-20261015.md) explains
Envoy #39650 and the difference between a workaround and a tested upstream fix.
