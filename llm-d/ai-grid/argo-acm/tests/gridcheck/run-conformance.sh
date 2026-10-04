#!/usr/bin/env bash
# Run the signals conformance check from this machine against every grid site.
# Each site's identity is read into a 0700 temp dir, never printed, and removed on exit.
#   SITES="dagobah site-d" ./run-conformance.sh [--json out.json]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
kubeconfig=${GRID_KUBECONFIG:-$HOME/.kube/grid.kubeconfig}
sites=(${SITES:-dagobah site-d})
k() { kubectl --kubeconfig "$kubeconfig" --context "$1" -n grid "${@:2}"; }
secret() { k "$1" get secret "$2" -o "jsonpath={.data.${3//./\\.}}" | base64 -d; }

tmp=$(mktemp -d)
chmod 700 "$tmp"
trap 'rm -rf "$tmp"' EXIT
umask 077

args=()
for site in "${sites[@]}"; do
  mkdir -p "$tmp/$site"
  secret "$site" grid-site-identity tls.crt > "$tmp/$site/tls.crt"
  secret "$site" grid-site-identity tls.key > "$tmp/$site/tls.key"
  ip=$(k "$site" get svc grid-operator-swim -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
  port=$(k "$site" get svc grid-operator-swim -o jsonpath='{.spec.ports[?(@.name=="signals")].port}')
  args+=(--site "$site=$ip:${port:-9091}" --identity "$site=$tmp/$site")
done
# The CA the GridNetwork names, which every site's identity chains to.
ca_secret=$(k "${sites[0]}" get gridnetwork -o jsonpath='{.items[0].spec.tls.caSecretRef.name}')
secret "${sites[0]}" "${ca_secret:-grid-ca}" ca.crt > "$tmp/ca.crt"
python3 "$here/signals_conformance.py" "${args[@]}" --ca "$tmp/ca.crt" "$@"
