# AI-Grid demo status

## Working end-to-end (verified on dagobah)

Both instances work. They demonstrate different rate-limit mechanisms.

**v2** (ext_proc: Envoy + praxis-extproc `ipp-v3.8`), namespace `ai-grid-v2`:
- Auth: no token or bad token gets 401.
- Geo routing: region claim fences to the in-region site (verify-geo 4/4).
- Rate limiting: `rate_limit` filter, per-identity REQUEST rate, trips 429 under burst.
- Route: `https://gateway-ai-grid-v2.apps.dagobah.hexfusion.local`

**v3** (pure gateway `praxis-ai-gateway:0.6.0-quota-v2`), namespace `ai-grid-v3`:
- Auth: no token gets 401, valid HS256 JWT gets 200.
- Rate limiting: `token_rate_limit` + `token_count`, per-subject TOKEN budget. Trips 429
  once cumulative usage crosses the budget (100 tokens at ~16/req trips at request 7).
  Per-subject isolation confirmed: over-budget subject stays 429 while fresh subjects
  get 200.
- Geo: not yet. This image's intelligent_route uses match_claims/overlay, not the
  hand-written region candidates v2's ipp-v3.8 accepts, so region geo needs the
  operator overlay (or match_claims labels).
- Route: `https://praxis-gateway-v3-ai-grid-v3.apps.dagobah.hexfusion.local`

Both images are proven on this cluster (ipp-v3.8 is the live grid gateway; 0.6.0-quota-v2
is the v3-scratch token config from the backup, "Config B").

## Rate-limit variant: request-rate vs token-quota

v2 rate limits by request rate (the `rate_limit` filter, per identity), not by token
budget. That is what the available images ship and what the live `grid-system`
gateway also uses.

Token-budget limiting (`token_rate_limit` reconciled against `token_count`) is an
experimental feature behind the `token-rate-limit-filter` cargo flag. Enabling it is
necessary but not sufficient: a gateway built with only that flag registers the token
filter but not the core `policy` filter, so it cannot authenticate. The `policy`
filter is a praxis-core filter whose registration is not in the default
`praxis-ai-proxy` build path, so a token-budget gateway needs a build recipe that
enables both the core policy filter and the experimental token filter. That recipe is
the follow-up. The Limitador quota policy plugin is not the path: its debit never
fires on the inference path (see below).

## Not the path (learned this session)

- The pure-gateway + policy quota/limitador plugin (praxis-policy #116/#117) does not
  meter on the inference path: the debit never fires (the N1/N2 gap in
  QUOTA-METERING-HANDOFF.md). Do not chase it for the demo.
- Geo on the newer 0.6.0/0.3.0 gateway image is overlay-driven (operator generates
  the routing candidates from GridSite/InferenceProvider CRs); its intelligent_route
  candidates use `site`/`stable_id`, not a hand-written `region` field. The live
  `grid-system` deployment is the reference for that model.

## Also standing

- Payload provenance pipeline: `payload/` builds images pinned to git hashes with the
  composing PRs stamped as OCI labels.
- Live `grid-system` grid demo (separate): operator + overlay + Envoy/extproc gateway
  routing to real 30B backends, load-aware routing + per-identity request rate. Works
  (401 without a token, 200 with).
