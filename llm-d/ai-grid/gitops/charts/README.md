# Shared charts

| Chart | Purpose | Inputs |
|---|---|---|
| `bootstrap/` | Argo project and root Application | Site configuration |
| `applications/` | Child Applications for models, gateway and observability | Site and gateway configuration |
| `local-models/` | LLMInferenceServices, serving gateway, identity and scoped grants | Model definition followed by site overrides |

Run from the canonical `gitops/` directory:

```sh
helm lint charts/bootstrap -f sites/dagobah/site.yaml
helm lint charts/applications -f sites/dagobah/site.yaml -f sites/dagobah/gateway.yaml
helm lint charts/local-models -f models/qwen3-coder-30b-a3b/dagobah.yaml -f sites/dagobah/site.yaml
helm template dagobah charts/applications -f sites/dagobah/site.yaml -f sites/dagobah/gateway.yaml
```

Argo value-file paths are relative to the chart directory. Bootstrap creates the root;
it does not install the platform prerequisites or import remote clusters. Review rendered
resources and perform server-side admission checks before applying an installation.
