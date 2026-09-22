# ai-grid v3

Pure praxis gateway in one process: JWT auth, geo residency routing (match_claims
on candidate labels), and per-subject token-budget rate limiting. Self-contained:
this directory has the chart, config, values, image build, and token minter.

Image: `praxis-ai-gateway:geo-labels-token-v1` (ai `@45c67142` / PR #1283 +
`--features token-rate-limit-filter`, 0.6.0 core). Source and provenance: IMAGES-BOM.md.

## The image

Pullable from quay: `quay.io/sbatsche/praxis-ai-gateway:geo-labels-token-v1`
(the mock backends pull `ghcr.io/praxis-proxy/grid-mock-providers:v0.1.1`). Nothing
to build, just deploy.

To rebuild it from the pinned git hash (reproducible, stamps PR labels):

    ./payload/build.sh geo-labels     # see payload/manifest.toml and IMAGES-BOM.md

## Deploy (raw Helm)

    kubectl create namespace ai-grid-v3

    kubectl create configmap v3-praxis-config -n ai-grid-v3 \
      --from-file=praxis.yaml=config/praxis.yaml \
      --from-file=policy.yaml=config/policy.yaml

    kubectl apply -n ai-grid-v3 -f mock-backends.yaml

    helm upgrade --install v3-gateway charts/praxis-gateway -n ai-grid-v3 \
      --set fullnameOverride=v3-gateway \
      --set image.repository=quay.io/sbatsche/praxis-ai-gateway \
      --set image.tag=geo-labels-token-v1 \
      --set config.existingConfigMap=v3-praxis-config \
      --set-string service.port=8080

    # ingress: OpenShift route, or port-forward on plain Kubernetes
    oc create route edge v3-gateway --service=v3-gateway --port=8080 -n ai-grid-v3
    # kubectl port-forward svc/v3-gateway 8080:8080 -n ai-grid-v3

`gateway-values.yaml` holds the same settings as a values file if you prefer
`-f gateway-values.yaml` over `--set`.

## Create a token

The gateway trusts an HS256 JWT. `sub` keys the token budget, `grid_region` is the
residency claim geo fences on. Secret/issuer/audience are the demo defaults
(`ai-grid-demo-secret-change-me`, `https://grid.internal/idp`, `ai-grid`); override
with `GRID_JWT_SECRET`/`GRID_JWT_ISSUER`/`GRID_JWT_AUDIENCE`.

    ./mint-region-tokens.py --mint alice us-east-1   # alice, routes to us-east-1
    ./mint-region-tokens.py --mint bob               # no region: fails closed (403)

In production the token comes from a real IdP (Keycloak); this script stands in.

## Use it

    URL=https://$(oc get route v3-gateway -n ai-grid-v3 -o jsonpath='{.spec.host}')
    TOK=$(./mint-region-tokens.py --mint alice us-east-1)
    curl -sk -X POST "$URL/v1/chat/completions" \
      -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
      -d '{"model":"Qwen3-Coder-30B-A3B","messages":[{"role":"user","content":"hi"}]}'

Behavior: no token or a bad token is 401. A token with no `grid_region` is 403 (geo
fails closed). A subject past its budget is 429 (per subject). Change the region to
route to a different site.
