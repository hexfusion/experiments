# AI-grid demo: image bill of materials

Every image in the v2/v3 demo, traced to its source. The payload pipeline
(`payload/`) is the mechanism: it builds images pinned to git hashes and stamps
the composing PRs as OCI labels, so provenance is the PR list, not a bare SHA.

## v3 consumer gateway (current)

`quay.io/sbatsche/praxis-ai-gateway:geo-labels-token-v1`

- Source: github.com/praxis-proxy/ai at `45c67142` (branch `feat/candidate-claim-gating`)
- PR: #1283 (match_claims entitlement routing with candidate labels)
- Core: praxis-proxy-filter 0.6.0 (this is what "0.6.0 core" means; the 0.6.0 core
  includes the policy filter, which the 0.5.4 core does not)
- Build: `cargo build --release -p praxis-ai-proxy --features token-rate-limit-filter`
- Provides: JWT auth (policy filter, PPE user-jwt), geo routing (match_claims on
  candidate labels), token-budget rate limiting (token_rate_limit + token_count),
  load-aware routing capability (intelligent_route signals_endpoint, unused with mocks)
- Binary: praxis-ai

## v2 gateway (Envoy) + ext_proc

- `docker.io/envoyproxy/envoy:v1.31-latest` (the data plane; ext_proc client only)
- `quay.io/sbatsche/praxis-extproc:ipp-v3.8` (the policy/routing decision point)
  - Revision: `3c32d7c63d617fc21bc66bb65a893d8db99096da`, image version 9.8, built 2026-09-08
  - Source: github.com/praxis-proxy/ai (load-datasource lineage)
  - Provides: JWT auth (policy), geo routing (region_claim + region candidates),
    request-rate limiting (rate_limit filter), token_count
  - This is the same image the live grid-system gateway runs, so it is proven on cluster
  - Provenance gap: this image carries only a git revision label, no PR mapping, the
    exact problem the payload pipeline fixes

## Backends

`ghcr.io/praxis-proxy/grid-mock-providers:v0.1.1`, OpenAI-shaped mock inference
(returns usage, which token_count reads for the token budget).

## Prior builds (payload pipeline, labeled)

- `praxis-ai-gateway:0.6.0-quota-v2`, 0.6.0 core + token_rate_limit + policy. Config B
  from the v3-scratch backup; ran the token-budget demo before geo-labels-token-v1.
- `praxis-ai-gateway:geo-quota-v1`, ai match_claims (#1283) + policy quota plugin
  (#116/#117) stitched. OCI labels: `dev.hexfusion.payload.base=ai-match-claims@45c67142`,
  `dev.hexfusion.payload.policy=policy-quota@7a2ebe0`. Not used: the quota-plugin debit
  does not fire on the inference path.

## Official images (grid Helm charts)

- `ghcr.io/praxis-proxy/ai:0.3.0`, official gateway (0.6.0 core, production feature set)
- `ghcr.io/praxis-proxy/grid-operator:v0.1.4`, operator (overlay generation)

## How to regenerate the BOM

Each payload-built image stamps `dev.hexfusion.payload.*` OCI labels
(base@hash, policy@hash, PR numbers). Read them with:

    skopeo inspect docker://<image> | jq '.Labels | with_entries(select(.key|startswith("dev.hexfusion")))'

For non-payload images, the git revision is in `org.opencontainers.image.revision`.
