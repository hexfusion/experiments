#!/usr/bin/env bash
# Stand the grid up on the three AWS clusters. Idempotent enough to re-run.
set -euo pipefail
CHART=${CHART:-$HOME/.cache/grid-wt/split-svc/charts}
KCDIR=${KCDIR:-$HOME/.local/share/aws-sno-grid}
ENROLL_URL=https://enrollment.apps.hub.aws.hexfusion.io
OP_IMG=quay.io/sbatsche/grid-operator
OP_DIGEST=sha256:77cb71c30225aabf3c4893ab513fc0b230c18d2d16b9e3dad26338e190d537c2
kc() { export KUBECONFIG="$KCDIR/$1/auth/kubeconfig"; }

echo "== CRDs first: the chart renders them beside the resources that use them =="
for s in hub site-1 site-2; do kc "$s"
  for k in gridnetwork gridsite inferenceprovider agenttoolprovider; do
    helm template grid-operator "$CHART/grid-operator" -n grid-system \
      --show-only "templates/crds/$k.yaml" | oc apply -f - >/dev/null
  done
  echo "  $s: $(oc get crd 2>/dev/null | grep -c grid.praxis.fast)/4"
done

echo "== operators =="
for s in hub site-1 site-2; do
  case $s in
    hub)    enroll=false ;;
    *)      enroll=true ;;
  esac
  kc "$s"
  helm upgrade --install grid-operator "$CHART/grid-operator" -n grid-system --create-namespace \
    --set crds.enabled=false --set platform=aws \
    --set swim.siteName="$s" --set grid.id=aws --set grid.peerTrust=spiffe \
    --set enrollment.enabled=$enroll --set enrollment.url="$ENROLL_URL" \
    --set signals.enabled=true \
    --set swim.service.enabled=true --set swim.service.type=LoadBalancer \
    --set image.repository="$OP_IMG" --set image.digest="$OP_DIGEST" \
    --timeout 6m >/dev/null
  echo "  $s: installed"
done

echo "== wait for both Services to get addresses =="
for s in hub site-1 site-2; do kc "$s"
  for svc in grid-operator-swim grid-operator-signals; do
    until [ -n "$(oc get svc $svc -n grid-system -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null)" ]; do sleep 10; done
  done
  echo "  $s: swim and signals addressed"
done

echo "== seeds and signals addresses: ingress hostnames, not NAT egress (issue 302) =="
declare -A SWIM SIG
for s in hub site-1 site-2; do kc "$s"
  SWIM[$s]=$(oc get svc grid-operator-swim -n grid-system -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
  SIG[$s]=$(oc get svc grid-operator-signals -n grid-system -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
done
for s in hub site-1 site-2; do
  peers=""
  for p in hub site-1 site-2; do [ "$p" = "$s" ] || peers="${peers:+$peers,}${SWIM[$p]}:7946"; done
  kc "$s"
  helm upgrade grid-operator "$CHART/grid-operator" -n grid-system --reuse-values \
    --set-json "swim.seeds=\"$peers\"" \
    --set-json "signals.advertiseAddress=\"${SIG[$s]}:9091\"" --timeout 5m >/dev/null
  echo "  $s: seeds set, signals advertised as ${SIG[$s]}:9091"
done

echo "== membership =="
kc hub
until [ "$(oc get gridsite --no-headers 2>/dev/null | wc -l)" -ge 3 ]; do sleep 10; done
oc get gridsite
