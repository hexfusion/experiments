#!/usr/bin/env bash
# Stand up site-d: a peer inference site on the LAN.
#
#   ./up.sh                 real inference on CPU (default)
#   BACKEND=sim ./up.sh     the llm-d simulator, starts in seconds
#
# Idempotent. Writes its own kubeconfig rather than touching ~/.kube/config.
set -euo pipefail
cd "$(dirname "$0")"

CLUSTER=site-d
BACKEND="${BACKEND:-vllm-cpu}"
MODEL="${MODEL:-Qwen/Qwen3-0.6B}"
KCFG="$PWD/site-d.kubeconfig"
LAN_IP="${LAN_IP:-$(ip -4 route get 1.1.1.1 2>/dev/null | grep -oE 'src [0-9.]+' | awk '{print $2}')}"

# Rootless podman cannot delegate cgroups here (Delegate=no), so kind runs
# rootful against the system socket. That needs sudo, and sudo asks on the
# terminal.
KIND_ENV=(env PATH="$PATH" CONTAINER_HOST=unix:///run/podman/podman.sock
          KIND_EXPERIMENTAL_PROVIDER=podman)
kind_root() { sudo -E "${KIND_ENV[@]}" kind "$@"; }

say() { printf '\n== %s\n' "$*"; }

if [ ! -t 0 ] && ! sudo -n true 2>/dev/null; then
  echo "This needs sudo and has no terminal to ask on. Run it from a shell." >&2
  exit 1
fi

say "cluster"
if kind_root get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "   exists, reusing"
  kind_root export kubeconfig --name "$CLUSTER" --kubeconfig "$KCFG" >/dev/null
else
  kind_root create cluster --config kind-site-d.yaml --kubeconfig "$KCFG"
fi
sudo chown "$(id -u):$(id -g)" "$KCFG"
chmod 600 "$KCFG"
K=(kubectl --kubeconfig "$KCFG")

# kind nodes on this host cannot reach a registry: a pull inside the node
# hangs with no error. The working pattern here is to pull into the rootful
# podman store the node shares and load the image in.
say "staging the image into the node"
IMAGE="$(BACKEND="$BACKEND" MODEL="$MODEL" ./manifests.sh | awk '/^          image:/{print $2; exit}')"
echo "   $IMAGE"
if sudo podman image exists "$IMAGE"; then
  echo "   already in the rootful store"
else
  sudo podman pull "$IMAGE"
fi
sudo -E "${KIND_ENV[@]}" kind load docker-image "$IMAGE" --name "$CLUSTER"

say "workload (backend=$BACKEND model=$MODEL)"
BACKEND="$BACKEND" MODEL="$MODEL" ./manifests.sh | "${K[@]}" apply -f -

say "waiting for the model to serve"
"${K[@]}" -n site-d rollout status deploy/model --timeout=1200s

say "firewall"
NEED=""
for p in 30080 30443 30091; do
  firewall-cmd --query-port="${p}/tcp" >/dev/null 2>&1 || NEED="$NEED $p"
done
if [ -n "${NEED// /}" ]; then
  echo "   not open:$NEED"
  echo "   peers cannot reach site-d until you run:"
  echo "     for p in$NEED; do sudo firewall-cmd --add-port=\$p/tcp --permanent; done"
  echo "     sudo firewall-cmd --reload"
else
  echo "   30080 30443 30091 open"
fi

say "site-d is up"
cat <<EOF
   kubeconfig       $KCFG
   model endpoint   http://${LAN_IP}:30080/v1
   metrics          http://${LAN_IP}:30080/metrics
   reserved         ${LAN_IP}:30443 provider gateway (mTLS)
                    ${LAN_IP}:30091 signals

   verify from a peer:
     curl -s http://${LAN_IP}:30080/v1/models
EOF
