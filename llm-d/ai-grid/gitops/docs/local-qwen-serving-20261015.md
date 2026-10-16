# Local Qwen services through GitOps

`dagobah` owns the `dagobah-local-models` child Application. Both use manual sync
during qualification, without automatic pruning or cascading deletion.
The child uses the existing default AppProject, as do the lab's shared services.

The child owns three namespaces, a dedicated HTTPS serving Gateway and its
service configuration, one Grid service account with a Kubernetes-populated token
Secret, the `dagobah-local` GridNetwork, and two LLMInferenceServices.
Token bytes and generated InferenceProviders are not stored in Git.

| LLMInferenceService | Node | GPU |
|---|---|---|
| qwen-local-a/qwen3-coder-30b-a3b | endor.dagobah.hexfusion.local | RTX A5000, 1 |
| qwen-local-b/qwen3-coder-30b-a3b | worker2.dagobah.hexfusion.local | RTX A5000, 1 |

Both advertise `qwen3-coder-30b-a3b`. Separate namespaces preserve distinct
publisher endpoints, avoiding a shared KServe routing group for both instances.
Weights use the same AWQ variant as the existing remote canary, whose Hugging Face
repository moved from cpatonn to cyankiwi. The revision is pinned in lab values.
Initial limits are 8192 tokens, eight sequences and eager execution.

KServe creates an InferencePool and EPP for each service. The merged ODH model
controller must create one cluster-scoped InferenceProvider per service/Gateway
pair, plus a model-scoped Role and RoleBinding. Publication follows usable
KServe-reported Gateway attachments and addresses, not merely the API create.

The ODH KServe payload's default GPU runtime is Red Hat AI Inference Server 3.2.4,
not an ODH-built runtime image. This provenance is explicit in the inventory.
EPP and storage initializer use ODH builds. All images and weights are pinned.

The dedicated Gateway leaves the existing MaaS and data-science Gateways intact.
This stage validates model serving and automatic provider publication.
The new GridNetwork initially has no gatewayRefs: connecting the existing manual
Grid canary to generated provider routing remains a separate acceptance step.
Model files currently use KServe's pod-local storage; pod replacement downloads
the pinned weights again.

## Validation

Argo created both services at 2026-10-16T01:07:34Z. The model controller generated
two InferenceProviders with matching source UIDs, distinct publisher URLs and the
configured service-account credential reference. Both EPPs and InferencePools
became ready. Runtime model startup and protected inference remain under test.

The first download on Endor was OOMKilled (exit 137) with a 4 GiB initializer
limit. The chart now disables the optional HF Xet downloader and sets an 8 GiB
initializer limit / 1 GiB request; the effect is pending validation. This is a
lab setting, not a universal model sizing recommendation. See the
[Hugging Face variable documentation](https://huggingface.co/docs/huggingface_hub/package_reference/environment_variables#hfhubdisablexet).
The chart also explicitly sets the certificate reference group and omits an empty
capability list, matching API normalization without hiding spec differences.

Each instance has one exclusive GPU and no spare GPU on its selected node.
Set `rolloutStrategy.maxSurge: 0` and `maxUnavailable: 1`; the default Deployment
surge leaves replacement pods Pending while old pods hold the GPUs. This means
an individual instance is unavailable during replacement. The limit is declared
on LLMInferenceService, not patched onto its generated Deployment.


The [publication audit](../tests/local-serving/publication-audit.json) verifies
both generated provider identities, endpoints, model names, credentials and
model-scoped grants. The [writer handoff](docs-writer-handoff-20261016.md) records
the prerequisites, namespace ownership, runtime provenance and remaining gates.

Grid's platform role does not read Secrets in arbitrary model credential
namespaces. The chart grants its ServiceAccount only `get` on the named token
Secret; without that grant the Grid provider reconciler returns Forbidden even
though ODH model controller successfully published the provider.

With one replica and `maxUnavailable: 1`, Deployment Available and KServe Ready
can be True while the only model container is initializing. Require actual model
container readiness and successful inference. The replacement download pods have
started on their assigned GPUs, with zero restarts as of 01:30 UTC; inference is
not yet verified.
