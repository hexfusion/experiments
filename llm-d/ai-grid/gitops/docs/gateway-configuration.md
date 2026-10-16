# Configure the Grid gateway through Helm and Argo CD

The current hub gateway is configured in `sites/dagobah/gateway.yaml` under `gridGateway`.
The root Application renders `dagobah-grid-gateway`, which uses the upstream
Praxis gateway chart from a pinned ODH source commit. Argo renders Helm and
applies its resources. It does not run `helm upgrade` or update Helm release
history.

The first adoption uses exactly the values read from the existing `grid-gateway`
Helm release. Its namespace, Deployment and Service names remain `grid/grid-gateway`.
Existing listener certificates, Grid identity and hop-validation Secrets are
referenced by name. No credential bytes are copied into Git.

## Routine configuration changes

1. Edit `gridGateway.values` in the environment values file. Typical changes are
   `replicaCount`, `resources`, `log.level`, `networkPolicy.from` and the pinned
   `image.digest`. Change `gridGateway.chart.revision` only as an explicit chart
   upgrade, after reviewing its values migration.
2. Render the Application and lint the selected chart with the proposed values.
   The Application template rejects mutable chart revisions and image tags and
   preserves the existing Service name. Review the resulting workload diff in
   Argo, including mount and network-policy changes.
3. Commit, push, and sync `dagobah` at that commit to update the child Application.
   Sync `dagobah-grid-gateway` to apply its updated Helm inputs. Sync is manual
   during qualification; pruning and cascading deletion are not enabled.
4. Confirm the deployed image and configuration, ready replicas, and an actual
   streaming request through MaaS. Argo Healthy alone is not inference evidence.

From the repository root:

```sh
helm lint llm-d/ai-grid/gitops/charts/applications -f llm-d/ai-grid/gitops/sites/dagobah/site.yaml -f llm-d/ai-grid/gitops/sites/dagobah/gateway.yaml
helm template dagobah llm-d/ai-grid/gitops/charts/applications -f llm-d/ai-grid/gitops/sites/dagobah/site.yaml -f llm-d/ai-grid/gitops/sites/dagobah/gateway.yaml
kubectl --context default/api-dagobah-hexfusion-local:6443/kube:admin \
  get applications.argoproj.io dagobah dagobah-grid-gateway -n openshift-gitops
kubectl --context default/api-dagobah-hexfusion-local:6443/kube:admin \
  rollout status deployment/grid-gateway -n grid --timeout=120s
```

Use the Argo UI to inspect the workload diff and sync. After adoption, Argo is
the writer for these resources; the old Helm release history is retained as a
historical record. Do not alternate `helm upgrade` with Argo sync or use
`helm uninstall` to remove that history, because uninstall deletes live resources.

## Rollout and rollback behavior

This chart hashes its rendered or inline Praxis configuration into the pod
template. Changes to that configuration roll the gateway; replica and resource
changes use the Deployment's normal update behavior. Secret references remain
separate from Secret contents. Referencing a pre-existing ConfigMap does not
automatically make its edits a Helm-triggered rollout.

Rollback is a Git revert of the configuration commit followed by the same root
and child sync. It is not `helm rollback`, because Argo owns the desired state.
Verify streaming again after rollback.

## Ownership as the integration evolves

| Configuration | Owner and user input |
|---|---|
| Gateway workload, listener, resources, image and network policy | Helm values in Git |
| Model deployment and its KServe routing | LLMInferenceService in Git |
| Model publication and model-scoped access grants | ODH model controller |
| Provider credentials and trust material | Named Secrets; external to Git |
| Generated provider topology and delegated mounts | Grid operator, when enabled |
| MaaS model catalog | ExternalModel referencing only Grid for Grid-managed models |

The current canary still uses `config.inline` for its authenticated hub-to-site-d
route. Moving that configuration into Git does not connect the two new local
providers. The next integration step must preserve each published endpoint's
base path, inject its final-hop credential, and validate TLS. The proposed runtime pin is Praxis AI commit
`7acd82b421f61c0a8ccd91c58479aacbd34cb08d` with Praxis `0.7.4`. It is a
candidate, not the deployed image. That runtime separates health and metrics
from the admin API: configure `admin.metrics_address` and target probes there.
Qualify the chart/probe migration, Grid endpoint-path rendering, and an ODH image
before enabling this path.

Upstream [Grid PR #233](https://github.com/praxis-proxy/grid/pull/233) introduces
explicit BYO, operator and render sources, with a breaking values migration.
Track that existing work instead of introducing another ownership scheme here.
The deployed chart stays pinned until the migration and its runtime are qualified.
