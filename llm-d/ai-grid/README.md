# AI Grid demo (dagobah)

Reproducible deploy of the AI-grid control plane: the grid **frontdoor**
(smart routing across sites) and the **ai-grid-demo** ext_proc lane (per-tenant
token limiting). The multi-tenancy story: two consumers, `alice` (gold) and
`bob` (copper), share the grid and are isolated by rate at the frontdoor and by
priority at the site EPP.

## What's here

    kustomization.yaml   the deployable demo (grid-system + ai-grid-demo)
    namespaces.yaml
    grid-system/         frontdoor, grid-operator, epp-metrics-relay, tempo (+cfg, route)
    ai-grid-demo/        praxis-extproc-grid, the two envoys (+cfg)
    flowcontrol/         site EPP per-band admission (InferenceObjectives + scheduler) — identified; staged
    subscriptions/       site-level MaaS subscriptions (provider layer) — under review
    plans.yaml           gold/silver/copper: rate (frontdoor) + priority band (EPP)
    helpers/             mint-tokens.sh, recreate.sh, TUNNEL.md
    BRANCHES.md          which branch each image is built from
    SECRETS.md           the mTLS/CA secrets (prerequisite, not committed)

## Prerequisites (out of band — not created by this package)

- The mTLS/CA **secrets** (SECRETS.md) in both namespaces.
- The **site tenants** (`ai-tenant-site-a/b`, vLLM + EPP) — the model backends.
- The **cloudflared tunnel** for `gateway.hexfusion.io` (helpers/TUNNEL.md) — host-side, leave running.

## Deploy

    kubectl apply -k .

## Tear down + recreate (preserves secrets, sites, tunnel)

    ./helpers/recreate.sh

This deletes only the demo objects; it leaves the namespaces (so the secrets in
them survive), the site tenants, and the cloudflared tunnel untouched.

## Roll out a rebuilt image

Rebuild from the branch in BRANCHES.md, push, then bump its tag in
`kustomization.yaml` (`images:`) and `kubectl apply -k .`.

## Mint user tokens

A consumer authenticates to the grid frontdoor with an HS256 JWT (the
`user-jwt` filter). The token carries the consumer's plan: `sub` (identity),
`tier`, and the metering budget `grid_rate`/`grid_burst` that the frontdoor's
`rate_limit` filter debits per identity. `plans.yaml` is the single source of
truth -- the token inherits `rate`/`burst` from the named plan, so you never
hand-set limits.

Mint tokens with the self-contained minter (pure stdlib, no PyJWT/yaml):

    # default: alice=gold, bob=copper
    GRID_JWT_SECRET=<frontdoor HS256 secret> ./helpers/mint-tokens.sh

    # any user=plan pairs (plans: gold, silver, copper -- see plans.yaml)
    GRID_JWT_SECRET=... ./helpers/mint-tokens.sh alice=silver carol=gold

`GRID_JWT_SECRET` must match the frontdoor policy's `decoding_key`; when unset
the minter falls back to the demo default (`ai-grid-demo-secret-change-me`).
It prints a table of user / plan / rate / burst / token.

Use the token as a bearer credential to the frontdoor:

    curl -sk https://<frontdoor-host>/v1/chat/completions \
      -H "Authorization: Bearer <token>" \
      -H "Content-Type: application/json" \
      -d '{"model":"Qwen3-Coder-30B-A3B","messages":[{"role":"user","content":"hi"}]}'

For flow control, add the site-EPP objective header so the request lands in a
priority band: `-H "x-llm-d-inference-objective: gold"` (or silver/copper).

## Demo the multi-tenancy

    GRID_JWT_SECRET=... ./helpers/mint-tokens.sh alice=gold bob=copper
    # hit the frontdoor as bob until copper's burst is spent -> 429,
    # while alice on gold keeps getting 200 in the same window. Tenant isolation.

## Held (features, after reproducibility lands)

- flow-control-aware smart routing (consume per-band EPP metrics; class-aware scoring)
- geo/locality routing (topology scorer, à la llm-d-router#2347; descriptor `selection_tier`)
