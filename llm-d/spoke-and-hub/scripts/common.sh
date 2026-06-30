#!/usr/bin/env bash
# Shared vars + helpers for the POC #3 kind topology (1 hub + 2 spokes).
set -euo pipefail

HUB=aig-hub
SPOKE_A=aig-spoke-a
SPOKE_B=aig-spoke-b
SPOKE_C=aig-spoke-c
CLUSTERS=("$HUB" "$SPOKE_A" "$SPOKE_B" "$SPOKE_C")
SPOKES=("$SPOKE_A" "$SPOKE_B" "$SPOKE_C")

# MetalLB L2 band per cluster: starting last-octet, +9 => 10 IPs each. Non-overlapping, high end
# of the shared kind subnet to dodge node-IP IPAM. Works for podman /24 and docker /16 (see lb_prefix).
declare -A LB_BAND=( [$HUB]=200 [$SPOKE_A]=210 [$SPOKE_B]=220 [$SPOKE_C]=230 )

# Overridable pins. KIND_NODE_IMAGE unset => kind's bundled default.
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-}"
METALLB_VERSION="${METALLB_VERSION:-v0.14.8}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # repo root (parent of lib/)
KIND_DIR="$HERE/cluster-setup/kind"

ctx() { echo "kind-$1"; }                       # kind prefixes kubeconfig contexts with kind-
k()   { kubectl --context "$(ctx "$1")" "${@:2}"; }

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "missing prerequisite: $1"; }

# Detect podman-emulated docker CLI and pin kind's provider so kind + kind_run agree.
if [ -z "${KIND_EXPERIMENTAL_PROVIDER:-}" ] && docker --version 2>/dev/null | grep -qi podman; then
  export KIND_EXPERIMENTAL_PROVIDER=podman
fi

# kind under rootless podman needs systemd Delegate=yes; run it in a delegated user scope.
# No-ops to a plain call when not rootless-podman or systemd-run is unavailable.
kind_run() {
  if [ "${KIND_EXPERIMENTAL_PROVIDER:-}" = podman ] && command -v systemd-run >/dev/null 2>&1; then
    systemd-run --user --scope -q -p "Delegate=yes" kind "$@"
  else
    kind "$@"
  fi
}

# IPv4 subnet of the shared kind network (created on first cluster). Handles podman (.Subnets)
# and docker (.IPAM.Config) inspect schemas. e.g. "10.89.0.0/24" or "172.18.0.0/16".
kind_net_subnet() {
  local s
  s="$(podman network inspect kind --format '{{range .Subnets}}{{.Subnet}} {{end}}' 2>/dev/null \
      | tr ' ' '\n' | grep -E '^[0-9]+\.' | head -1)" || true
  [ -n "$s" ] || s="$(docker network inspect kind \
      -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null \
      | tr ' ' '\n' | grep -E '^[0-9]+\.' | head -1)" || true
  [ -n "$s" ] || die "kind network has no IPv4 subnet (create a cluster first)"
  echo "$s"
}

# First-three-octet base the LB band's last octet hangs off. /<=16 => X.Y.255, else => X.Y.Z.
lb_prefix() {
  local subnet plen o1 o2 o3
  subnet="$(kind_net_subnet)"
  plen="${subnet#*/}"; IFS=. read -r o1 o2 o3 _ <<<"${subnet%/*}"
  if [ "${plen:-24}" -le 16 ]; then echo "$o1.$o2.255"; else echo "$o1.$o2.$o3"; fi
}
