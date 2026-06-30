#!/usr/bin/env bash
# Substrate: kind clusters (1 hub + 3 spokes) on one shared network, each with a MetalLB band so the
# hub can reach the spokes. CoreDNS patched for rootless-podman host-DNS pollution.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../scripts" && pwd)/common.sh"
need kind; need kubectl; need docker

inst="$(sysctl -n fs.inotify.max_user_instances 2>/dev/null || echo 0)"
[ "${inst:-0}" -ge 512 ] || warn "fs.inotify.max_user_instances=$inst is low; if a node hangs: sudo sysctl -w fs.inotify.max_user_instances=512"
keys="$(cat /proc/sys/kernel/keys/maxkeys 2>/dev/null || echo 0)"
[ "${keys:-0}" -ge 1000 ] || warn "kernel.keys.maxkeys=$keys is low for ${#CLUSTERS[@]} clusters; if pods stick in ContainerCreating (session keyring quota exceeded): sudo sysctl -w kernel.keys.maxkeys=5000 kernel.keys.maxbytes=2000000"

# 1. Clusters (shared kind network gives cross-cluster routing).
for c in "${CLUSTERS[@]}"; do
  if kind get clusters 2>/dev/null | grep -qx "$c"; then
    log "cluster $c exists"
  else
    log "creating $c"
    kind_run create cluster --name "$c" --config "$KIND_DIR/${c#aig-}.yaml" ${KIND_NODE_IMAGE:+--image "$KIND_NODE_IMAGE"}
  fi
done

# 2. MetalLB + a non-overlapping L2 pool per cluster.
LBP="$(lb_prefix)"
log "shared subnet $(kind_net_subnet); LB bands on ${LBP}.x"
for c in "${CLUSTERS[@]}"; do
  band="${LB_BAND[$c]}"; lo="${LBP}.${band}"; hi="${LBP}.$((band + 9))"
  if ! k "$c" -n metallb-system get deploy controller >/dev/null 2>&1; then
    log "[$c] installing MetalLB $METALLB_VERSION"
    k "$c" apply -f "https://raw.githubusercontent.com/metallb/metallb/${METALLB_VERSION}/config/manifests/metallb-native.yaml"
  fi
  k "$c" -n metallb-system rollout status deploy/controller --timeout=180s
  k "$c" -n metallb-system wait --for=condition=ready pod -l component=controller --timeout=120s
  log "[$c] L2 pool ${lo}-${hi}"
  for n in 1 2 3 4 5 6; do   # webhook can lag readiness; retry until it sticks
    k "$c" apply -f - >/dev/null 2>&1 <<EOF && break
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata: { name: aig-pool, namespace: metallb-system }
spec: { addresses: [ "${lo}-${hi}" ] }
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata: { name: aig-l2, namespace: metallb-system }
spec: { ipAddressPools: [ aig-pool ] }
EOF
    [ "$n" -lt 6 ] || die "[$c] pool apply failed"; sleep 5
  done
done

# 3. CoreDNS: sink bogus host TLDs that poison cluster FQDN lookups (rootless-podman).
for c in "${CLUSTERS[@]}"; do
  core="$(k "$c" -n kube-system get cm coredns -o jsonpath='{.data.Corefile}')"
  block=""
  for tld in ${BOGUS_TLDS:-piplet dns.podman}; do
    case "$core" in *"$tld:53"*) :;; *) block+="${tld}:53 { template ANY ANY { rcode NXDOMAIN } }
";; esac
  done
  [ -n "$block" ] || { log "[$c] CoreDNS ok"; continue; }
  log "[$c] sinking bogus TLDs"
  k "$c" -n kube-system create cm coredns --from-literal=Corefile="${block}${core}" --dry-run=client -o yaml | k "$c" -n kube-system apply -f - >/dev/null
  k "$c" -n kube-system rollout restart deploy/coredns >/dev/null
  k "$c" -n kube-system rollout status deploy/coredns --timeout=90s
done

log "substrate up. next: scripts/1-spokes.sh"
