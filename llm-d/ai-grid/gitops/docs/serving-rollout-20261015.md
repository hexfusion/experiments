# Dagobah serving rollout — 2026-10-15

The three custom images are deployed, KServe is Managed and Ready, and live JSON
and incremental SSE pass through MaaS → hub Grid → site-d Grid → Qwen. The Envoy
image and all eight EnvoyFilter specifications match the pre-rollout baseline.
This qualifies the existing remote canary; protected KServe/EPP inference remains open.

## Observed state

- Platform operator: three Ready replicas at `quay.io/opendatahub/opendatahub-operator@sha256:295eecb00205a90006b371c28aa8ab3954f2c6c2dc15515cb95fe65ec679a58d`.
- Custom KServe module, Praxis and AI Gateway controller: Ready at the immutable
  digests in [the inventory](image-inventory.md).
- KServe controller, LLMInferenceService controller and merged #966 model controller: Ready.
- DSC, AIGateway, AIGrid, ModelsAsAService and KServe conditions: True.
- Live model-controller ClusterRole grants create/delete/get/list/patch/update/watch
  on `grid.praxis.fast/inferenceproviders`.
- No LLMInferenceService, InferencePool or InferenceProvider objects are configured yet.
- MaaS exposes the tested Qwen name through ExternalProvider/grid only.

See [running image IDs and status](../evidence/2026-10-16/serving-payload/live-rollout-status.json).
The new Argo workload root still does not own this manual installation.

## Streaming evidence

| Request | HTTP | Observation |
|---|---|---|
| JSON, normal upload | 200 | OpenAI completion |
| JSON, chunked upload | 200 | OpenAI completion |
| SSE, normal upload | 200 | First event 116 ms; final event 223 ms |
| SSE, chunked upload | 200 | First event 100 ms; final event 207 ms |
| Missing API key | 401 | Rejected |
| Invalid API key | 401 | Rejected |

Both streams include completion content and `[DONE]`. The short-lived test key
was revoked successfully (HTTP 200). The probe used certificate-verified TLS through
a local port-forward to the actual MaaS Service, followed by the real Grid hops.
It did not change routing objects or Envoy configuration.

Envoy reports `06f9604a19dcdece315aea66ce7941746c826a6d/1.34.2-dev/3.1.0/RELEASE/OpenSSL`.
The post-processor configuration now explicitly sets request body mode `streamed`
and response body mode `none`. See [results](../evidence/2026-10-16/serving-payload/live-grid-streaming-result.json)
and [the probe](../tests/serving-payload/probe_live_grid.py).

## Rollout adjustments

Pushes use `quay.io:443`; deployment pulls use `quay.io`. All three custom images
became accessible anonymously and with the existing cluster credentials after
the repository access correction. [Pull evidence](../evidence/2026-10-16/serving-payload/cluster-pull-access.json).

Restarting the platform exposed a missing old registry manifest, digest `f6a8ecec…`.
The old three replicas stayed available during recovery. The replacement came from
[successful build 38766072224](https://github.com/opendatahub-io/opendatahub-operator/actions/runs/38766072224), PR head
`17b556f299d356aea4bd744df65ee22d054678c4`, build commit `710e8cc7cc945fbae5b95ccd31e072cbf369789b`.
Its base branch also changed TLS startup timeout, auth-proxy handling and the
framework dependency; the exact source-tree changes are recorded in
[verification evidence](../evidence/2026-10-16/serving-payload/platform-refresh-verification.json).
Existing related-image overrides were retained. A digest-preserving local copy
was saved for recovery. The old missing digest is not a usable registry rollback target.
This does not claim all upstream CI checks passed.

The Praxis image propagates through the platform owner to a ConfigMap-backed
environment variable in AI Gateway controller. The existing controller needed a
restart to read it before the configuration-producing controller image was upgraded.
That restart exposed startup OOM kills at the existing 256 MiB memory limit.
Adding `GOMEMLIMIT=192MiB` allowed startup and reconciliation; the setting survived
the owner-driven controller image update. It is a manual additive Deployment
override and still needs durable manifest packaging.
[Memory evidence](../evidence/2026-10-16/serving-payload/controller-memory-adjustment.json).

KServe was enabled after its image pins reached the platform owner. Its first
reconciliation retried while newly installed webhook endpoints became ready, then
reported Ready. The [applied plan](../evidence/2026-10-16/serving-payload/rollout/plan.json) records
execution order, guarded patches and the remaining qualification gate.

## Remaining acceptance

1. Create a real LLMInferenceService with a pinned ODH runtime; validate InferencePool/EPP,
   namespace/Gateway publishing and the resulting Grid InferenceProvider.
2. Validate Grid credentials and authorization at the protected serving gateway,
   including local and remote routes. This canary's last hop is plaintext HTTP to vLLM.
3. Package the controller memory setting and establish GitOps ownership.
4. Complete quota/revocation enforcement, model publication, enrollment and identity
   lifecycle acceptance listed in [the payload](../payload/selection.yaml).

The overall payload remains assembling; these results do not qualify a complete
ODH runtime payload, fresh installation, or protected EPP path.
