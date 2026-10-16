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

Pending Argo sync and live validation. Verify both services, pools, EPPs and
runtime pods; source UID annotations on each generated provider; distinct
endpoints; credential references; model-scoped grants; and no provider authored
by Argo. Record observed image IDs and inference results before qualification.
