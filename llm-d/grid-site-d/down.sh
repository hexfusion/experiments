#!/usr/bin/env bash
# Tear down site-d. Leaves firewall rules in place.
set -euo pipefail
cd "$(dirname "$0")"
sudo -E env PATH="$PATH" CONTAINER_HOST=unix:///run/podman/podman.sock \
  KIND_EXPERIMENTAL_PROVIDER=podman kind delete cluster --name site-d
rm -f site-d.kubeconfig
echo "site-d deleted. Firewall ports left open; remove with:"
echo "  for p in 30080 30443 30091; do sudo firewall-cmd --remove-port=\$p/tcp --permanent; done"
echo "  sudo firewall-cmd --reload"
