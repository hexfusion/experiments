#!/usr/bin/env bash
# Each cluster gets its own credential. Deploys Keycloak, creates one service-account client per cluster
# (its access token IS that cluster's pool credential), and writes them to Secret llm-d/pool-credentials.
# Also creates tenant-1 users + a 'maas' front-door client for the future Authorino layer.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
need kubectl; need curl; need python3; need podman

NS="${NS:-llm-d}"
REALM="${REALM:-llm-d}"
KC_ADMIN="${KC_ADMIN:-admin}"; KC_ADMIN_PW="${KC_ADMIN_PW:-admin}"; USER_PW="${USER_PW:-password}"
TOKEN_LIFESPAN="${TOKEN_LIFESPAN:-36000}"   # 10h so demo tokens outlive the run
KEYCLOAK_IMAGE="${KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0}"
LPORT="${LPORT:-18080}"
POOLS=(cluster-a cluster-b); USERS=(alice bob)
HUB_CTX="$(ctx "$HUB")"; KC="http://127.0.0.1:${LPORT}"

# deploy Keycloak
podman image exists "$KEYCLOAK_IMAGE" || { log "pulling $KEYCLOAK_IMAGE"; podman pull -q "$KEYCLOAK_IMAGE"; }
kind load docker-image "$KEYCLOAK_IMAGE" --name "$HUB"
k "$HUB" apply -f "$HERE/manifests/auth/keycloak.yaml"
k "$HUB" -n keycloak rollout status deploy/keycloak --timeout=240s

# port-forward (torn down on exit)
kubectl --context "$HUB_CTX" -n keycloak port-forward svc/keycloak "${LPORT}:8080" >/dev/null 2>&1 &
trap 'kill $! >/dev/null 2>&1 || true' EXIT
for n in $(seq 1 30); do curl -fsS -o /dev/null "${KC}/realms/master" && break; sleep 1; [ "$n" -lt 30 ] || die "keycloak unreachable"; done

# REST helpers
jget() { python3 -c 'import sys,json;print(json.load(sys.stdin).get(sys.argv[1],""))' "$1"; }
TOK="$(curl -fsS -X POST "${KC}/realms/master/protocol/openid-connect/token" \
  -d grant_type=password -d client_id=admin-cli -d "username=${KC_ADMIN}" -d "password=${KC_ADMIN_PW}" | jget access_token)"
[ -n "$TOK" ] || die "no admin token"
api()     { curl -fsS -H "Authorization: Bearer ${TOK}" -H 'Content-Type: application/json' "$@"; }
post_ok() { local c; c="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer ${TOK}" -H 'Content-Type: application/json' "$1" -d "$2")"; case "$c" in 201|409) ;; *) warn "POST $1 -> $c";; esac; }
uuid()    { api "${KC}/admin/realms/${REALM}/clients?clientId=$1" | python3 -c 'import sys,json;a=json.load(sys.stdin);print(a[0]["id"] if a else "")'; }

# realm
log "realm ${REALM}"
post_ok "${KC}/admin/realms" "{\"realm\":\"${REALM}\",\"enabled\":true,\"accessTokenLifespan\":${TOKEN_LIFESPAN}}"
api -X PUT "${KC}/admin/realms/${REALM}" -d "{\"accessTokenLifespan\":${TOKEN_LIFESPAN}}" >/dev/null

# one service-account client per cluster -> mint its token = the pool credential
declare -A POOL_TOKEN
for p in "${POOLS[@]}"; do
  cid="pool-${p}"; log "pool client ${cid}"
  post_ok "${KC}/admin/realms/${REALM}/clients" "{\"clientId\":\"${cid}\",\"enabled\":true,\"protocol\":\"openid-connect\",\"publicClient\":false,\"serviceAccountsEnabled\":true,\"standardFlowEnabled\":false,\"directAccessGrantsEnabled\":false}"
  u="$(uuid "$cid")"; [ -n "$u" ] || die "client ${cid} missing"
  secret="$(api -X POST "${KC}/admin/realms/${REALM}/clients/${u}/client-secret" | jget value)"
  [ -n "$secret" ] || secret="$(api "${KC}/admin/realms/${REALM}/clients/${u}/client-secret" | jget value)"
  POOL_TOKEN[$p]="$(curl -fsS -X POST "${KC}/realms/${REALM}/protocol/openid-connect/token" \
    -d grant_type=client_credentials -d "client_id=${cid}" -d "client_secret=${secret}" | jget access_token)"
  [ -n "${POOL_TOKEN[$p]}" ] || die "no token for ${cid}"
done

# tenant-1 users + front-door client (ready for Authorino later)
for u in "${USERS[@]}"; do
  post_ok "${KC}/admin/realms/${REALM}/users" "{\"username\":\"${u}\",\"enabled\":true,\"emailVerified\":true,\"attributes\":{\"tenant\":[\"tenant1\"]},\"credentials\":[{\"type\":\"password\",\"value\":\"${USER_PW}\",\"temporary\":false}]}"
done
post_ok "${KC}/admin/realms/${REALM}/clients" '{"clientId":"maas","enabled":true,"protocol":"openid-connect","publicClient":true,"standardFlowEnabled":true,"directAccessGrantsEnabled":true}'

# write the pool credentials Secret
k "$HUB" create namespace "$NS" --dry-run=client -o yaml | k "$HUB" apply -f - >/dev/null
args=(); for p in "${POOLS[@]}"; do args+=(--from-literal="${p}=${POOL_TOKEN[$p]}"); done
k "$HUB" -n "$NS" create secret generic pool-credentials "${args[@]}" --dry-run=client -o yaml | k "$HUB" apply -f -

log "auth done: Secret ${NS}/pool-credentials. next: scripts/4-ipp.sh (re-run to refresh tokens)"
