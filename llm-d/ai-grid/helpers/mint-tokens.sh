#!/usr/bin/env bash
# Mint consumer tokens for the grid frontdoor. Each token is an HS256 JWT
# carrying the plan's grid_rate/grid_burst (from ../plans.yaml), which the
# frontdoor's rate_limit filter meters per identity.
#
#   GRID_JWT_SECRET=<frontdoor HS256 secret> helpers/mint-tokens.sh
#   GRID_JWT_SECRET=... helpers/mint-tokens.sh alice=silver carol=gold
#
# GRID_JWT_SECRET must match the frontdoor policy's decoding_key. If unset, the
# minter falls back to the demo default ("ai-grid-demo-secret-change-me").
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
exec python3 "$HERE/mint-tokens.py" "$@"
