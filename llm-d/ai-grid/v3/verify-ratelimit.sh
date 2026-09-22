#!/usr/bin/env bash
# Verify per-subject token rate limiting on the v3 gateway (in-process
# token_rate_limit filter, key: authenticated_subject, 2000-token/hour budget).
#
#   ./verify-ratelimit.sh --url https://<v3 route>
#
# Drives one subject past its budget and asserts the gateway returns 429 once
# the window is exhausted, then confirms a second subject still gets 200 (the
# budget is per subject, not global).
set -uo pipefail

URL="" ; MODEL="Qwen3-Coder-30B-A3B" ; MAXTOK=600 ; TRIES=10
while [ $# -gt 0 ]; do
  case "$1" in
    --url) URL="$2"; shift 2;;
    --model) MODEL="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[ -n "$URL" ] || { echo "usage: $0 --url <v3-gateway-url> [--model NAME]" >&2; exit 2; }

HERE="$(cd "$(dirname "$0")" && pwd)"
eval "$("$HERE/mint-region-tokens.py" --env)"   # sets US=, EU=, UK=, NONE=

# One request; echoes the HTTP status.
send() { # <jwt>
  curl -sk -o /dev/null -w '%{http_code}' -X POST "$URL/v1/chat/completions" \
    -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"max_tokens\":$MAXTOK,\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}"
}

echo "subject US: draining a ${MAXTOK}-token estimate per request against a 2000 budget"
saw200=0 ; saw429=0 ; trip=0
for i in $(seq 1 "$TRIES"); do
  code="$(send "$US")"
  printf "  req %-2s -> %s\n" "$i" "$code"
  [ "$code" = "200" ] && saw200=1
  [ "$code" = "429" ] && { saw429=1; trip=$i; break; }
done

echo "subject EU (separate budget): $(send "$EU")   # expect 200"
eu="$(send "$EU")"

echo "-----------------------------------------------------------"
pass=1
[ "$saw200" = 1 ] || { echo "FAIL: never got a 200 before the limit"; pass=0; }
[ "$saw429" = 1 ] || { echo "FAIL: budget never tripped a 429 in $TRIES requests"; pass=0; }
[ "$eu" = "200" ] || { echo "FAIL: second subject was rate limited ($eu), budget is not per-subject"; pass=0; }
if [ "$pass" = 1 ]; then
  echo "PASS: US tripped 429 at request $trip; EU unaffected (per-subject budget)"
else
  exit 1
fi
