# Models

| Definition | Site | Instances | Runtime |
|---|---|---|---|
| [Qwen Coder 30B A3B, Dagobah](qwen3-coder-30b-a3b/dagobah.yaml) | Dagobah | `qwen-local-a`, `qwen-local-b` | One RTX A5000 each; pinned RHAIIS image and AWQ model revision |
| [Existing Qwen Coder canary](qwen3-coder-30b-a3b/site-d.yaml) | site-d | `grid-canary/qwen-canary` | Existing two-GPU vLLM deployment; manual ownership |

Dagobah's file supplies Helm model/runtime values. Site placement, namespaces, gateway
identity and node-specific driver settings are in `sites/dagobah/site.yaml`.
The site-d file is the existing Kubernetes manifest, not a Helm values file; it is
recorded for reproducibility and is not applied by the Dagobah root.

Only Grid is registered as the MaaS provider for these Grid-managed models. Individual
InferenceProviders are created by ODH Model Controller from LLMInferenceService resources;
do not commit copies of those generated objects as desired state.
