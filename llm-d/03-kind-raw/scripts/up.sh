#!/bin/bash
# Brings up the 03-kind-raw demo end-to-end:
#   1. kind cluster (rootless podman)
#   2. Gateway API CRDs (standard channel) + GIE CRDs
#   3. kgateway controller (with inferenceExtension enabled — the gateway
#      needs to know about InferencePool as a backendRef)
#   4. Build + load images into kind:
#      - bbr-mock (built from our Go code)
#      - llm-d-uds-tokenizer:baked (TinyLlama files baked in; sim demos
#        have no model dir for the UDS sidecar to share with vLLM)
#      - kgateway controller image (kind's containerd pulls fail under
#        rootless podman so we side-load)
#   5. Apply manifests in numerical order
#   6. Port-forward Gateway data plane → localhost:18080 (kind already
#      maps host 8080 → node 30080 for hostport, so we use 18080)
#
# Idempotent: re-running picks up where it left off.
#
# Rootless podman: kind needs systemd Delegate=yes so the cluster cgroup
# can move into the user slice. Self-rexec under systemd-run if not yet.
set -euo pipefail
if [[ "${KIND_DELEGATED:-}" != "1" ]]; then
  exec systemd-run --user --scope --property=Delegate=yes \
    env KIND_DELEGATED=1 "$0" "$@"
fi

# kind clusters consume ~5-10 inotify instances per running container; the
# Fedora default of 128 is too low and Envoy Gateway crashes with "too many
# open files". Raise to 8192 (kind docs recommend >=512) before bringing up.
NEED_INSTANCES=8192
NEED_WATCHES=524288
cur_instances=$(cat /proc/sys/fs/inotify/max_user_instances)
cur_watches=$(cat /proc/sys/fs/inotify/max_user_watches)
if (( cur_instances < NEED_INSTANCES )) || (( cur_watches < NEED_WATCHES )); then
  cat <<EOF

== inotify limits too low ==
Current:  max_user_instances=$cur_instances, max_user_watches=$cur_watches
Required: max_user_instances=$NEED_INSTANCES, max_user_watches=$NEED_WATCHES

About to run (sudo will prompt):
    sudo sysctl -w fs.inotify.max_user_instances=$NEED_INSTANCES \\
                  fs.inotify.max_user_watches=$NEED_WATCHES

EOF
  sudo sysctl -w fs.inotify.max_user_instances=$NEED_INSTANCES fs.inotify.max_user_watches=$NEED_WATCHES
fi

# kind nodes are rootless and can't modprobe; iptable_nat must be loaded on
# the host so kube-proxy inside the kind container can write Service-IP NAT
# rules. Without this, ClusterIP traffic times out.
if ! grep -q "^ip_tables " /proc/modules; then
  cat <<EOF

== iptables kernel modules not loaded ==
kind's kube-proxy can't write Service NAT rules from inside a rootless
container. About to run (sudo will prompt):
    sudo modprobe ip_tables iptable_nat iptable_filter

EOF
  sudo modprobe ip_tables iptable_nat iptable_filter
fi

CLUSTER=llm-d-03
SCRIPTS=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
DIR=$(cd "$SCRIPTS/.." && pwd)
ROOT=$(cd "$DIR/.." && pwd)

echo "== 1. kind cluster =="
if ! kind get clusters | grep -q "^${CLUSTER}$"; then
  kind create cluster --config "$DIR/kind/cluster.yaml"
else
  echo "  cluster ${CLUSTER} already exists; skipping"
fi
kubectl config use-context "kind-${CLUSTER}"

echo
echo "== 2. CRDs (versions match llm-d well-lit-paths guide) =="
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/standard-install.yaml
kubectl apply -k https://github.com/kubernetes-sigs/gateway-api-inference-extension/config/crd?ref=v1.5.0

# CoreDNS picks up the kind node's resolv.conf search list, which includes
# `dns.podman piplet`. With ndots:5 + a wildcard-rewriting upstream DNS
# (Comcast, captive portals, some routers), every search-expanded query
# returns a valid-looking but wrong A record, hijacking ClusterIP→pod
# resolution. Patch CoreDNS to NXDOMAIN those zones explicitly.
echo
echo "== 2a. Patch CoreDNS to NXDOMAIN podman/piplet search domains =="
kubectl patch cm -n kube-system coredns --type=merge -p '
data:
  Corefile: |
    podman:53 piplet:53 {
        template ANY ANY . {
            rcode NXDOMAIN
        }
    }
    .:53 {
        errors
        health {
            lameduck 5s
        }
        ready
        kubernetes cluster.local in-addr.arpa ip6.arpa {
            pods insecure
            fallthrough in-addr.arpa ip6.arpa
            ttl 30
        }
        prometheus :9153
        forward . /etc/resolv.conf {
            max_concurrent 1000
        }
        cache 30 {
            disable success cluster.local
            disable denial cluster.local
        }
        loop
        reload
        loadbalance
    }
'
kubectl rollout restart -n kube-system deployment/coredns
kubectl wait --for=condition=available -n kube-system deployment/coredns --timeout=60s

echo
echo "== 3. Build + load images =="
make -C "$ROOT" 03-kind-raw/bbr-mock/bin/bbr-mock

# bbr-mock image (built locally)
podman build -t llm-d-bbr-mock:dev -f "$DIR/bbr-mock/Containerfile" "$ROOT"
podman save llm-d-bbr-mock:dev -o /tmp/bbr-mock.tar
kind load image-archive /tmp/bbr-mock.tar --name "$CLUSTER"
rm -f /tmp/bbr-mock.tar

# UDS tokenizer with TinyLlama files pre-baked.
podman build -t localhost/llm-d-uds-tokenizer:baked "$DIR/uds-image"
podman save localhost/llm-d-uds-tokenizer:baked -o /tmp/uds-baked.tar
kind load image-archive /tmp/uds-baked.tar --name "$CLUSTER"
rm -f /tmp/uds-baked.tar

# agentgateway controller (control plane) + data plane images. Kind's
# containerd can be flaky pulling registries under rootless podman, so we
# side-load both.
for img in cr.agentgateway.dev/controller:v1.1.0 cr.agentgateway.dev/agentgateway:v1.1.0; do
  podman pull "$img"
  tar_path=/tmp/$(echo "$img" | tr '/:' '_').tar
  podman save "$img" -o "$tar_path"
  kind load image-archive "$tar_path" --name "$CLUSTER"
  rm -f "$tar_path"
done

echo
echo "== 4. agentgateway (per llm-d well-lit-paths guide; kgateway is deprecated) =="
helm install --create-namespace --namespace agentgateway-system --version v1.1.0 \
  agentgateway-crds oci://cr.agentgateway.dev/charts/agentgateway-crds 2>/dev/null \
  || echo "  agentgateway-crds already installed"
helm install --namespace agentgateway-system --version v1.1.0 \
  --set inferenceExtension.enabled=true \
  agentgateway oci://cr.agentgateway.dev/charts/agentgateway 2>/dev/null \
  || echo "  agentgateway already installed"
kubectl wait --for=condition=available deployment/agentgateway -n agentgateway-system --timeout=120s

echo
echo "== 5. Manifests =="
kubectl apply -f "$DIR/manifests/"

echo
echo "== 6. Wait for Gateway =="
kubectl wait --for=condition=Programmed gateway/llm-d -n llm-d --timeout=180s
kubectl wait --for=condition=Available -n llm-d deployment/epp --timeout=180s

echo
echo "== 7. Port-forward Gateway data plane → localhost:18080 =="
# kind already maps host 8080 → node 30080 (kind/cluster.yaml), so we use
# 18080 to avoid collision. PID stored for teardown.
PF_LOG=/tmp/llm-d-03-portforward.log
PF_PID_FILE=/tmp/llm-d-03-portforward.pid
pkill -f "kubectl.*port-forward.*svc/llm-d" 2>/dev/null || true
sleep 1
nohup kubectl port-forward -n llm-d svc/llm-d 18080:8080 >"$PF_LOG" 2>&1 &
echo $! > "$PF_PID_FILE"
echo "  port-forward PID: $(cat $PF_PID_FILE), log: $PF_LOG"

echo
echo "== Done. =="
echo "  Test:    ./test.sh   (hits localhost:18080)"
echo "  Logs:    kubectl logs -n llm-d -l app=epp -c epp -f"
echo "  Tear:    ./teardown.sh"
