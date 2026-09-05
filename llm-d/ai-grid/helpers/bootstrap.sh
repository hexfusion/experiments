#!/usr/bin/env bash
# Trust bootstrap. Issues the grid's mTLS material from ONE certificate authority
# -- the operator-minted hub-ca (grid-system) -- so every peer chains to the same
# root. Nothing here is restored from a backup; run it against a clean cluster
# after the GridNetwork CR exists (the operator mints hub-ca from it).
#
# It creates:
#   grid-system/hub-swim-key        32-byte SWIM gossip key (stops the operator loop)
#   ai-grid-demo/grid-ca            the client-trust root (= hub-ca's cert)
#   ai-grid-demo/grid-envoy-server-tls  the envoy's server cert, signed by hub-ca
#   <out>/client.{crt,key} + ca.crt a peer client cert (SPIFFE SAN) for testing mTLS
#
#   KUBECTL=oc CONTEXT=<ctx> helpers/bootstrap.sh [peer-site]   # default peer: site-a
set -euo pipefail

KUBECTL="${KUBECTL:-oc}"
CTX=(); [ -n "${CONTEXT:-}" ] && CTX=(--context "$CONTEXT")
PEER="${1:-site-a}"                                   # SPIFFE peer identity to mint
SPIFFE="spiffe://grid.internal/site/${PEER}"
OUT="${OUT:-$(cd "$(dirname "$0")/.." && pwd)/.trust}"; mkdir -p "$OUT"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

kc() { "$KUBECTL" "${CTX[@]}" "$@"; }
apply_secret() { kc apply -f - >/dev/null; }         # stdin = a secret manifest

echo "==> extracting the signing CA (operator-minted hub-ca)"
kc -n grid-system get secret hub-ca -o jsonpath='{.data.ca\.crt}' | base64 -d > "$TMP/ca.crt"
kc -n grid-system get secret hub-ca -o jsonpath='{.data.ca\.key}' | base64 -d > "$TMP/ca.key"
[ -s "$TMP/ca.crt" ] && [ -s "$TMP/ca.key" ] || { echo "hub-ca has no ca.crt/ca.key -- apply the GridNetwork CR first so the operator mints it"; exit 1; }

sign() { # $1=subj-CN  $2=SAN  $3=out-basename
  openssl genrsa -out "$TMP/$3.key" 2048 >/dev/null 2>&1
  openssl req -new -key "$TMP/$3.key" -subj "/O=ai-grid/CN=$1" -out "$TMP/$3.csr" >/dev/null 2>&1
  openssl x509 -req -in "$TMP/$3.csr" -CA "$TMP/ca.crt" -CAkey "$TMP/ca.key" -CAcreateserial \
    -days 365 -sha256 -extfile <(printf 'subjectAltName=%s\n' "$2") -out "$TMP/$3.crt" >/dev/null 2>&1
}

echo "==> SWIM gossip key -> grid-system/hub-swim-key"
head -c 32 /dev/urandom > "$TMP/swim.key"
kc -n grid-system create secret generic hub-swim-key --from-file=key="$TMP/swim.key" \
  --dry-run=client -o yaml | apply_secret

echo "==> client-trust root -> ai-grid-demo/grid-ca (= hub-ca cert)"
kc -n ai-grid-demo create secret generic grid-ca --from-file=ca.crt="$TMP/ca.crt" \
  --dry-run=client -o yaml | apply_secret

echo "==> envoy server cert (signed by hub-ca) -> ai-grid-demo/grid-envoy-server-tls"
sign "grid-envoy" "DNS:grid-envoy.ai-grid-demo.svc.cluster.local,DNS:grid-envoy.ai-grid-demo.svc,DNS:grid-envoy" server
kc -n ai-grid-demo create secret tls grid-envoy-server-tls --cert="$TMP/server.crt" --key="$TMP/server.key" \
  --dry-run=client -o yaml | apply_secret

echo "==> frontdoor server cert (signed by hub-ca) -> grid-system/grid-gateway-server-tls"
sign "grid-gateway" "DNS:grid-gateway.grid-system.svc.cluster.local,DNS:grid-gateway.grid-system.svc,DNS:grid-gateway" gwserver
kc -n grid-system create secret tls grid-gateway-server-tls --cert="$TMP/gwserver.crt" --key="$TMP/gwserver.key" \
  --dry-run=client -o yaml | apply_secret

echo "==> enrollment CA (= hub-ca) + join token list -> grid-system/enrollment-ca"
# The enrollment service signs joiners with the SAME CA as the operator (single
# root). operators.txt is the BYOT join-token list -- one `name:token` line per
# authorized operator. Replace the demo token with a real secret.
printf 'admin:%s\n' "${GRID_JOIN_TOKEN:-demo-operator-token}" > "$TMP/operators.txt"
kc -n grid-system create secret generic enrollment-ca \
  --from-file=ca-cert.pem="$TMP/ca.crt" --from-file=ca-key.pem="$TMP/ca.key" --from-file=operators.txt="$TMP/operators.txt" \
  --dry-run=client -o yaml | apply_secret

echo "==> peer client cert (SPIFFE $SPIFFE) -> $OUT/client.*"
sign "$PEER" "URI:$SPIFFE" client
cp "$TMP/client.crt" "$TMP/client.key" "$TMP/ca.crt" "$OUT/"

echo "==> restart the envoy so it picks up the new server cert + trust root"
kc -n ai-grid-demo rollout restart deploy/grid-envoy >/dev/null 2>&1 || true

echo "==> deploy the enrollment endpoint (hub-only BYOT join surface)"
HERE_DIR="$(cd "$(dirname "$0")" && pwd)"
VALUES="${GRID_VALUES:-$HERE_DIR/../values.dagobah.env}"
if [ -f "$VALUES" ]; then
  "$HERE_DIR/render.sh" "$VALUES" steps/01-enrollment | kc apply -f - >/dev/null 2>&1 \
    && echo "  enrollment deployed" \
    || echo "  (enrollment deploy failed -- check the grid-enrollment image is pushed)"
else
  echo "  (skipped: set GRID_VALUES=<values.env> to deploy enrollment)"
fi

cat <<EOF

Trust bootstrapped from hub-ca. Test mTLS through the gateway:
  # from an in-cluster pod (mount $OUT), or copy the three files in:
  curl --cacert ca.crt --cert client.crt --key client.key \\
    https://grid-envoy.ai-grid-demo.svc:8080/v1/chat/completions \\
    -H 'Content-Type: application/json' -H 'x-llm-d-inference-objective: gold' \\
    -d '{"model":"Qwen3-Coder-30B-A3B","messages":[{"role":"user","content":"hi"}],"max_tokens":16}'

Client identity presented to peer_identity_trust: $SPIFFE
EOF
