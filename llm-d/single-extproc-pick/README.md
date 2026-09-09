# single-extproc-pick

One gateway ext_proc that authenticates, authorizes, meters a durable token budget, and
picks a local llm-d instance, all in a single praxis filter chain, driven by the real MaaS
control plane and consumed through the MaaS UI. No Authorino, no Kuadrant, no wasm shim, and
no second component for routing. Then prove it holds at scale.

    MaaS UI ─┐                         MaaS control plane
             │  create subscription    maas-api (Postgres): mint token, hold subscription
             │  get token, see budget  maas-controller (operator): MaaSSubscription ->
             ▼                            praxis chain config + Limitador limits
    consumer ─> Envoy gateway -> praxis ext_proc (one chain):
                                   policy          (authn + model-access authz)
                                   quota/limitador (durable per-consumer token budget)
                                   epp_router      (pick a local llm-d instance)
                                 -> chosen llm-d endpoint (InferencePool / vllm)
                                   token_count     (response phase: debit the Limitador bucket)
                   Limitador (durable shared counter) <── check / report

The thesis: the whole MaaS enforcement-and-routing dataplane collapses into one ext_proc, and
the MaaS operator is the control plane that configures it from a subscription. Auth and the
token budget share the resolved identity, so there is no cross-component descriptor to keep in
sync, and the same chain that enforces also picks the backend. Limitador holds the counter so
the budget stays correct as the gateway scales out. The UI and the operator make it the real
product, not a synthetic rig.

Design and benchmark plan:
`~/projects/hexfusion/design/work/llm-d/ai-grid/maas-enforcement/` (DESIGN, BENCHMARK, FEATURE).

## The stack

Control plane (the real MaaS operator, reused, not stubbed):

    maas-api        mints tokens, holds subscriptions, Postgres-backed
    maas-controller the operator: reconciles a MaaSSubscription into the praxis chain config
                    and the Limitador limits (one subscription, one coherent enforcement config)
    MaaS UI         the consumer portal: create a subscription, get a token, watch the budget

Data plane (one ext_proc plus the shared counter and a local model):

    praxis ext_proc one chain: policy (authn+authz) + quota/limitador + epp_router + token_count
    Limitador       durable, standalone, per-consumer token counter
    llm-d instance  InferencePool + EPP + backend (inference-sim default, vLLM for realism)

## Starting point

Bootstrapped from the dagobah `ai-grid` package. Reused pieces already copied in:

    config/praxis.gateway.reference.yaml    the live frontdoor chain (policy + rate_limit
                                            meter:tokens + token_count + intelligent_route),
                                            from ai-grid/grid/config/gateway/praxis.yaml
    manifests/limitador/                    the durable, standalone Limitador, from
                                            ai-grid/observability/limitador/
    manifests/llmd-pick-reference/          the praxis-picks-a-local-llm-d pattern
                                            (InferencePool + EPP + gateway + praxis epp_router),
                                            from 05-praxis-epp/manifests/

Reused from their own repos (referenced, deployed, not copied here):

    maas-api, maas-controller   ~/projects/opendatahub-io/models-as-a-service
                                (the dagobah MaaS deploy is the starting config)
    MaaS UI (consumer portal)   ~/projects/opendatahub-io/odh-dashboard
                                (distributions/maas-consumer-portal, packages/maas)

## What to build (the deltas from the starting point)

1. The chain. Fold the reference chains into one praxis config: policy (authn + model_access
   authz), the `quota/limitador` plugin in place of the in-memory meter, `token_count` on the
   response, and `epp_router` to pick the local llm-d instance instead of the grid's cross-site
   `intelligent_route`.
2. The plugin. Build `quota/limitador` in the policy repo (spec:
   `~/personal/grid-token-quota-plugin-design.md`) and rebuild praxis-extproc to link it. The
   one piece of new code.
3. The operator output. Point maas-controller at this chain: reconcile a MaaSSubscription into
   the praxis config and the Limitador limits, and retire or gate the Authorino and Kuadrant
   path it currently carries. This is the polish the operator was going to get anyway.
4. The local llm-d instance. InferencePool + EPP + backend (inference-sim default).
5. Wiring. A `kustomization.yaml` deploying the gateway, praxis ext_proc, Limitador, the llm-d
   pool, maas-api, and maas-controller; the MaaS UI; and `scripts/up.sh` for one-shot bring-up.
6. The benchmark. Scale the ext_proc replicas, drive many consumers (subscriptions minted
   through the UI or maas-api) with guidellm, and show the budget holding across replicas
   (versus the in-memory leak), the added latency, and the throughput ceiling. See BENCHMARK.

## Open choices

- Backend: inference-sim (default) or real vLLM.
- Cluster: dagobah (default) or EKS for a larger run.
- Pick mechanism: praxis `epp_router` calling the llm-d EPP over HTTP (the 05-praxis-epp
  pattern, llm-d-native) versus praxis reading the InferencePool endpoints directly.

## Status

Scaffolded 2026-09-09. Reference pieces copied from the dagobah `ai-grid` package. The chain
merge, the `quota/limitador` plugin, the maas-controller output, the MaaS UI wiring, the
kustomization, and the benchmark rig are not built yet.
