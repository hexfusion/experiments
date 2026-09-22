#!/usr/bin/env bash
# Verify data-residency routing against a gateway, for v2 (ext_proc) or v3 (pure
# gateway). Mints the four region tokens, sends one request each, and asserts the
# served site matches the token's region.
#
#   ./verify-geo.sh --mode v2 --url https://grid-gateway-grid-system.apps.dagobah.hexfusion.local
#   ./verify-geo.sh --mode v3 --url https://<v3 route>
#
# The served site is read from the `x-grid-site` response header, which both
# gateways emit (v2 via Envoy header_mutation, v3 via the gateway's route
# metadata). Region-to-site map: us-east-1 -> site_a|site_b, eu-west-1 -> site_d,
# eu-west-2 -> site_c.
#
# The region-less token is the one case v2 and v3 differ, on purpose:
#   v2 has no residency claim to fence on, so it routes by load (a served site).
#   v3 fails closed: no region claim, no route (HTTP 404). That is the residency
#   gap v3 closes; the mode flag encodes the expectation so both are tested.
set -uo pipefail

MODE="" ; URL="" ; MODEL="Qwen3-Coder-30B-A3B"
while [ $# -gt 0 ]; do
  case "$1" in
    --mode) MODE="$2"; shift 2;;
    --url)  URL="$2";  shift 2;;
    --model) MODEL="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[ -n "$MODE" ] && [ -n "$URL" ] || { echo "usage: $0 --mode v2|v3 --url <gateway-url> [--model NAME]" >&2; exit 2; }
case "$MODE" in v2|v3) ;; *) echo "--mode must be v2 or v3" >&2; exit 2;; esac

HERE="$(cd "$(dirname "$0")" && pwd)"
eval "$(python3 "$HERE/mint-region-tokens.py" --env)"   # sets US= EU= UK= NONE=

# name -> allowed served sites (a pipe-separated allowlist). NONE depends on mode.
declare -A TOKEN=( [US]="$US" [EU]="$EU" [UK]="$UK" [NONE]="$NONE" )
declare -A EXPECT=( [US]="site-us-a|site-us-b" [EU]="site-eu" [UK]="site-uk" )
if [ "$MODE" = v3 ]; then EXPECT[NONE]="FAIL_CLOSED"; else EXPECT[NONE]="site-us-a|site-us-b|site-eu|site-uk"; fi

body() { printf '{"model":"%s","max_tokens":8,"messages":[{"role":"user","content":"ping"}]}' "$MODEL"; }

pass=0; fail=0
printf '%-5s %-10s %-14s %-14s %s\n' NAME REGION EXPECT GOT RESULT
printf -- '---------------------------------------------------------------\n'
for name in US EU UK NONE; do
  tok="${TOKEN[$name]}"; want="${EXPECT[$name]}"
  hdrs="$(curl -sk -o /dev/null -D - -m 60 \
      -H "Authorization: Bearer $tok" -H "Content-Type: application/json" \
      --data "$(body)" "$URL/v1/chat/completions" 2>/dev/null)"
  code="$(printf '%s' "$hdrs" | awk 'toupper($1) ~ /HTTP/ {print $2}' | tail -1)"
  site="$(printf '%s' "$hdrs" | awk -F': ' 'tolower($1)=="x-grid-site"{gsub(/\r/,"",$2);print $2}' | tail -1)"
  region="$(python3 - "$tok" <<'PY'
import base64,json,sys
p=sys.argv[1].split('.')[1]; p+='='*(-len(p)%4)
print(json.loads(base64.urlsafe_b64decode(p)).get('grid_region','(none)'))
PY
)"
  if [ "$want" = FAIL_CLOSED ]; then
    got="${code:-?}${site:+ ($site)}"
    if [ "$code" = 404 ] || [ "$code" = 403 ]; then res=PASS; else res=FAIL; fi
  else
    got="${site:-<none> (code ${code:-?})}"
    if printf '%s' "$site" | grep -qE "^(${want})$"; then res=PASS; else res=FAIL; fi
  fi
  [ "$res" = PASS ] && pass=$((pass+1)) || fail=$((fail+1))
  printf '%-5s %-10s %-14s %-14s %s\n' "$name" "$region" "$want" "$got" "$res"
done
printf -- '---------------------------------------------------------------\n'
printf 'mode=%s  pass=%d  fail=%d\n' "$MODE" "$pass" "$fail"
[ "$fail" -eq 0 ]
