# Operations

Use the Dagobah kubeconfig context for hub commands. The remote site-d is a separate cluster.

| Namespace on Dagobah | Components |
|---|---|
| `qwen-local-a`, `qwen-local-b` | Local Qwen engines and EPP schedulers |
| `qwen-serving` | Model-client identity and token Secret |
| `opendatahub` | KServe, LLMInferenceService, model, Grid and AI Gateway controllers |
| `opendatahub-operator-system` | ODH platform operator |
| `grid` | Hub Grid gateway |
| `openshift-ingress` | MaaS and local serving gateways; shared Praxis processors |
| `models-as-a-service` | MaaS registrations and ExternalModel Praxis processor |
| `openshift-gitops` | Argo CD and Applications |
| `open-cluster-management-observability` | ACM Thanos, Grafana and collectors |

On site-d, the gateway and remote model are in `grid-canary`; ACM observability components
are in `open-cluster-management-addon-observability`.

```sh
oc get pods -n qwen-local-a -o wide
oc get pods -n qwen-local-b -o wide
oc logs -n qwen-local-a deployment/qwen3-coder-30b-a3b-kserve -c main -f
oc get applications.argoproj.io -n openshift-gitops
```

A running engine, a Ready custom resource, successful authentication, and working
streaming inference are separate checks. The local models use one GPU each and allow
one unavailable replica during replacement. No model cache is persistent yet; replacing
a pod downloads the weights again.
