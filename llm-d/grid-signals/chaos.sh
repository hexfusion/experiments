#!/usr/bin/env bash
# Break the signals path on purpose, without killing anything.
#
# The grid e2e suite already has verify-failover-under-lost-peer, and it says
# in its own doc comment that the partition is "simulated by process kill, not
# real network-level isolation". That difference matters here. A killed
# operator stops answering and stops polling, so its peer sees a member that
# left. A reachable-but-unanswering one keeps running, keeps serving what it
# already holds, and keeps ageing, which is the state the staleness signals
# exist to report.
#
# Withdrawing one TCP port from the SWIM Service stages exactly that: SWIM is
# UDP, so membership still says the site is alive, while TCP 9091 stops being
# forwarded. It surfaces as outcome="refused" rather than a timeout, because
# the address still answers and declines rather than going silent. A partition
# that blackholes packets reads as "timeout" instead; both are worth staging
# and they are not the same failure.
#
#   ./chaos.sh cut pool-b            # pool-b's signals become unreachable
#   ./chaos.sh heal pool-b           # and reachable again
#   ./chaos.sh latency pool-a 250    # delay pool-a's traffic to its peers
#   ./chaos.sh latency-clear pool-a
#   ./chaos.sh blackhole pool-a      # drop it instead, so polls time out
#   ./chaos.sh blackhole-clear pool-a
#   ./chaos.sh status                # what each site can see and reach
set -euo pipefail

NS=grid-system
SITES=(pool-a pool-b pool-c)
ctx() { echo "kind-grid-llmd-pm-$1"; }
SWIM_PORT='{"name":"swim-udp","port":7946,"targetPort":"swim-udp","protocol":"UDP"}'
SIG_PORT='{"name":"signals","port":9091,"targetPort":"signals","protocol":"TCP"}'

patch_ports() {
  kubectl --context "$(ctx "$1")" -n "$NS" patch svc grid-operator-swim \
    --type=merge -p "{\"spec\":{\"ports\":[$2]}}"
}

# The addresses of every site but this one, as SWIM advertises them.
peer_addresses() {
  local me="$1" out="" ip
  for site in "${SITES[@]}"; do
    [ "$site" = "$me" ] && continue
    ip=$(kubectl --context "$(ctx "$site")" -n "$NS" get svc grid-operator-swim \
      -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
    [ -n "$ip" ] && out="${out} ${ip}"
  done
  printf '%s' "$out"
}

# Run a command in a node's network namespace.
#
# hostNetwork plus privileged, because tc has to see the node's interface
# rather than a pod's. One-shot: the qdisc it installs outlives the pod, which
# is the point.
#
# A manifest on stdin rather than --overrides, because the script is shell and
# quoting it through JSON on a command line is how a filter silently becomes
# the wrong filter.
run_on_node() {
  local site="$1" script="$2"
  local name="netem-$$-${RANDOM}"
  local image="${NETEM_IMAGE:-docker.io/nicolaka/netshoot:v0.13}"
  kubectl --context "$(ctx "$site")" -n "$NS" apply -f - >/dev/null <<MANIFEST
apiVersion: v1
kind: Pod
metadata: {name: ${name}, namespace: ${NS}}
spec:
  hostNetwork: true
  restartPolicy: Never
  containers:
    - name: netem
      image: ${image}
      securityContext: {privileged: true}
      command: ["sh", "-c"]
      args:
        - |
${script}
MANIFEST
  # Wait for it to finish, not for it to be ready. A one-shot pod is never
  # Ready, and on a first run the image pull takes longer than the command.
  local phase="" i=0
  while [ $i -lt 90 ]; do
    phase=$(kubectl --context "$(ctx "$site")" -n "$NS" get pod "${name}" \
      -o jsonpath='{.status.phase}' 2>/dev/null || true)
    case "$phase" in Succeeded|Failed) break ;; esac
    sleep 2; i=$((i + 1))
  done
  local out
  out=$(kubectl --context "$(ctx "$site")" -n "$NS" logs "${name}" 2>&1 | tail -3)
  kubectl --context "$(ctx "$site")" -n "$NS" delete pod "${name}" --ignore-not-found >/dev/null 2>&1
  printf '%s\n' "$out"
}

proxy() {
  kubectl --context "$(ctx "$1")" get --raw \
    "/api/v1/namespaces/${NS}/services/$2:$3/proxy/metrics" 2>/dev/null || true
}

case "${1:-}" in
  cut)
    patch_ports "${2:?site}" "$SWIM_PORT"
    echo "cut: ${2} signals unreachable, SWIM left alone"
    ;;
  heal)
    patch_ports "${2:?site}" "$SWIM_PORT,$SIG_PORT"
    echo "healed: ${2} signals reachable again"
    ;;
  latency)
    # Delay this site's traffic to its peers, and nothing else.
    #
    # A netem qdisc on the node's root would delay kubelet and API traffic too
    # and can destabilise the cluster, so the delay hangs off a prio band that
    # only peer addresses are filtered into. Everything else keeps the default
    # bands and is untouched.
    #
    # tc lives in the node's network namespace, so a hostNetwork pod that
    # applies it and exits leaves the rule in place.
    site="${2:?site}"; ms="${3:?milliseconds}"
    peers="$(peer_addresses "$site")"
    [ -z "$peers" ] && { echo "no peer addresses found" >&2; exit 1; }

    script="          set -e
          tc qdisc replace dev eth0 root handle 1: prio bands 4
          tc qdisc replace dev eth0 parent 1:4 handle 40: netem delay ${ms}ms"
    for ip in $peers; do
      script="${script}
          tc filter replace dev eth0 protocol ip parent 1:0 prio 4 u32 match ip dst ${ip}/32 flowid 1:4"
    done
    script="${script}
          tc qdisc show dev eth0"
    run_on_node "$site" "$script"
    echo "latency: ${site} -> [${peers} ] delayed ${ms}ms"
    ;;

  blackhole)
    # Drop packets to the peers instead of declining them.
    #
    # FORWARD as well as OUTPUT: the operator polls from a pod, so its packets
    # cross the node rather than originate on it, and an OUTPUT rule alone
    # catches nothing while looking exactly like it should.
    #
    # This is the other partition. Withdrawing the service port makes the
    # address answer and refuse, so a poll fails at once and reads as refused.
    # Dropping makes it go silent, so every attempt spends its full timeout
    # before failing and reads as timeout. The retry rules exist to tell those
    # apart, and only one of them costs the request budget.
    site="${2:?site}"
    peers="$(peer_addresses "$site")"
    [ -z "$peers" ] && { echo "no peer addresses found" >&2; exit 1; }
    script="          set -e"
    for ip in $peers; do
      script="${script}
          iptables -I FORWARD 1 -d ${ip}/32 -j DROP
          iptables -I OUTPUT 1 -d ${ip}/32 -j DROP"
    done
    script="${script}
          iptables -L FORWARD -n --line-numbers | head -4"
    run_on_node "$site" "$script"
    echo "blackhole: ${site} -> [${peers} ] dropped, no reply at all"
    ;;

  blackhole-clear)
    site="${2:?site}"
    peers="$(peer_addresses "$site")"
    script="          set -e"
    for ip in $peers; do
      script="${script}
          while iptables -D FORWARD -d ${ip}/32 -j DROP 2>/dev/null; do :; done
          while iptables -D OUTPUT -d ${ip}/32 -j DROP 2>/dev/null; do :; done"
    done
    script="${script}
          echo cleared"
    run_on_node "$site" "$script"
    echo "blackhole cleared on ${site}"
    ;;

  latency-clear)
    site="${2:?site}"
    run_on_node "$site" "          tc qdisc del dev eth0 root 2>/dev/null || true
          echo cleared"
    echo "latency cleared on ${site}"
    ;;

  status)
    printf '%-10s %-26s %s\n' SITE 'HOLDS SIGNALS FOR' 'REACHES'
    for site in "${SITES[@]}"; do
      sites=$(proxy "$site" grid-operator-signals 9091 \
        | grep -o 'grid_site="[a-z-]*"' | sort -u | sed 's/.*="//;s/"//' | tr '\n' ' ')
      up=$(proxy "$site" grid-operator-metrics 9090 \
        | awk '/^grid_collection_up/ && $NF==1 {print $0}' \
        | grep -o 'peer="[a-z-]*"' | sed 's/.*="//;s/"//' | tr '\n' ' ')
      printf '%-10s %-26s %s\n' "$site" "${sites:-none}" "${up:-none}"
    done
    ;;
  *)
    sed -n '2,18p' "$0"
    exit 2
    ;;
esac
