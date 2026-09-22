# ai-grid v3

Pure praxis gateway in one process: JWT auth, geo residency routing (match_claims
on candidate labels), and per-subject token-budget rate limiting. Namespace
`ai-grid-v3`, self-contained with mock backends.

Image: `praxis-ai-gateway:geo-labels-token-v1` (ai `@45c67142` / PR #1283 +
`--features token-rate-limit-filter`, 0.6.0 core). See `../IMAGES-BOM.md`.

## Deploy (Helm)

    helm upgrade --install v3-gateway ../helm/charts/praxis-gateway \
      -n ai-grid-v3 -f ../helm/dagobah/v3-gateway-values.yaml

## Create a token

The gateway accepts an HS256 JWT on the Authorization header. Two claims matter:
`sub` keys the token budget, and `grid_region` is the residency claim geo fences on.
Signing secret, issuer, and audience are the demo defaults the gateway trusts
(`ai-grid-demo-secret-change-me`, `https://grid.internal/idp`, `ai-grid`).

Mint one for any user and region:

    ./mint-region-tokens.py --mint alice us-east-1   # alice, routes to the us-east-1 site
    ./mint-region-tokens.py --mint bob               # no region: fails closed under geo (403)

Or the four preset region tokens (US, EU, UK, NONE):

    ./mint-region-tokens.py          # table
    ./mint-region-tokens.py --env    # US=... EU=... UK=... NONE=... for eval

Override the secret/issuer/audience with `GRID_JWT_SECRET`, `GRID_JWT_ISSUER`,
`GRID_JWT_AUDIENCE` if the gateway config uses different values. In production the
token comes from a real IdP (Keycloak, etc.); this script stands in for that.

## Use it

    URL=https://$(oc get route v3-gateway -n ai-grid-v3 -o jsonpath='{.spec.host}')
    TOK=$(./mint-region-tokens.py --mint alice us-east-1)
    curl -sk -X POST "$URL/v1/chat/completions" \
      -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
      -d '{"model":"Qwen3-Coder-30B-A3B","messages":[{"role":"user","content":"hi"}]}'

Expected: no token or a bad token is 401. A token with no `grid_region` is 403
(geo fails closed). A token past its budget is 429 (per subject).
