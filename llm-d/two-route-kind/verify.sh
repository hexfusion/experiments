#!/usr/bin/env bash
# Checks the claims the two-route topology is supposed to demonstrate.
# REQUESTS / CONCURRENCY override the load arm.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER="${CLUSTER:-two-route}"
NS=two-route
REQUESTS="${REQUESTS:-20000}"
CONCURRENCY="${CONCURRENCY:-1000}"

k() { kubectl --context "kind-$CLUSTER" "$@"; }
pass() { printf '\033[1;32m ok \033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mFAIL\033[0m %s\n' "$*"; FAILED=1; }
FAILED=0

GW="$(k -n $NS get svc demo-istio -o jsonpath='{.spec.clusterIP}')"
GWPOD="$(k -n $NS get pod -l gateway.networking.k8s.io/gateway-name=demo -o jsonpath='{.items[0].metadata.name}')"
curl_in() { k -n istio-system exec deploy/istiod -- curl -sS -m 15 "$@" 2>/dev/null; }

# A complete chat completion. EPP parses the body, so a partial one is rejected
# before scheduling; that rejection is itself evidence the request reaches EPP.
BODY='{"model":"llama-3.1-8b","messages":[{"role":"user","content":"hello"}]}' 

echo "== 1. a request reaches a sim through both routes"
OUT="$(curl_in -i -X POST -H 'Content-Type: application/json' \
  -d "$BODY" "http://$GW:80/v1/chat/completions")"
grep -q '^HTTP/1.1 200' <<<"$OUT" && pass "200" || fail "not 200: $(head -1 <<<"$OUT")"
grep -qi '^x-ipp-destination:' <<<"$OUT" && pass "IPP decided (route 1 ran)" || fail "no x-ipp-destination: route 1 did not run"
grep -qi '^x-served-by:' <<<"$OUT" && pass "a sim served it (route 2 dispatched)" || fail "no x-served-by: route 2 did not dispatch"

echo
echo "== 2. no ext_proc is configured in the data plane"
DYN="$(istioctl --context "kind-$CLUSTER" -n $NS proxy-config all "$GWPOD" -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin)
hits=[c.get('@type','').split('.')[-1] for c in d.get('configs',[]) if 'ext_proc' in json.dumps(c)]
print(','.join(h for h in hits if h!='BootstrapConfigDump'))
")"
[ -z "$DYN" ] && pass "ext_proc appears only in the bootstrap extension list, never configured" \
              || fail "ext_proc configured in: $DYN"

echo
echo "== 3. loop avoidance is match precedence, not negation"
istioctl --context "kind-$CLUSTER" -n $NS proxy-config route "$GWPOD" --name http.80 -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin)
rs=[r for rc in d for vh in rc.get('virtualHosts',[]) for r in vh.get('routes',[])]
if not rs: sys.exit('no routes')
first=rs[0]
hdrs=[h.get('name') for h in first.get('match',{}).get('headers',[])]
assert 'x-ipp-processed' in hdrs, 'the marker route is not first: %s' % hdrs
assert not rs[1].get('match',{}).get('headers'), 'the second route unexpectedly matches headers'
print('order:', [([h.get('name') for h in r.get('match',{}).get('headers',[])] or 'no headers') for r in rs])
" && pass "the one-header route is evaluated first" || fail "route order is wrong"

echo
echo "== 4. a client can forge the marker and skip the decider"
FORGED="$(curl_in -i -X POST -H 'x-ipp-processed: 1' \
  -H "x-gateway-destination-endpoint: $(k -n $NS get pod -l app=sim-a -o jsonpath='{.items[0].status.podIP}'):8000" \
  -d "$BODY" "http://$GW:80/v1/chat/completions")"
if grep -q '^HTTP/1.1 200' <<<"$FORGED" && ! grep -qi '^x-ipp-destination:' <<<"$FORGED"; then
  pass "confirmed reachable: strip the marker at ingress or policy is bypassable"
else
  fail "expected a forged marker to bypass IPP; it did not"
fi

echo
echo "== 5. EPP made the decision, not the fallback"
grep -qi '^x-decided-how: epp' <<<"$OUT" && pass "IPP consulted EPP over the HTTP transport" \
  || fail "x-decided-how was not epp: the round-robin fallback ran"
EPOD="$(k -n $NS get pod -l app=epp -o jsonpath='{.items[0].status.podIP}')"
picks() { curl_in -m 15 "http://$EPOD:9090/metrics" | awk -F' ' '/inference_extension_plugin_duration_seconds_count.*max-score-picker/ {print $NF}'; }
BEFORE="$(picks)"; [ -n "$BEFORE" ] && pass "EPP picker counter readable ($BEFORE so far)" || fail "no picker metric"

echo
echo "== 6. load: $REQUESTS requests at concurrency $CONCURRENCY"
k -n $NS delete pod load --ignore-not-found >/dev/null 2>&1
k -n $NS run load --restart=Never --image=localhost/two-route:dev --overrides="{\"spec\":{\"containers\":[{\"name\":\"load\",\"image\":\"localhost/two-route:dev\",\"imagePullPolicy\":\"IfNotPresent\",\"env\":[{\"name\":\"MODE\",\"value\":\"load\"},{\"name\":\"GATEWAY_URL\",\"value\":\"http://$GW:80\"},{\"name\":\"REQUESTS\",\"value\":\"$REQUESTS\"},{\"name\":\"CONCURRENCY\",\"value\":\"$CONCURRENCY\"}]}]}}" >/dev/null
k -n $NS wait --for=jsonpath='{.status.phase}'=Succeeded pod/load --timeout=600s >/dev/null 2>&1
k -n $NS logs load | tail -9
k -n $NS logs load | grep -q 'WARNING' && fail "traffic did not spread" || pass "spread across sims"

AFTER="$(picks)"
DELTA=$(( ${AFTER%.*} - ${BEFORE%.*} ))
# A 200 with a plausible body proves nothing on its own: the picker counter is
# what shows EPP scheduled every request rather than something falling back.
[ "$DELTA" -ge "$REQUESTS" ] && pass "EPP scheduled all of it (picker +$DELTA)" \
  || fail "picker only moved by $DELTA for $REQUESTS requests"

echo
[ "$FAILED" = 0 ] && printf '\033[1;32mall checks passed\033[0m\n' || printf '\033[1;31msome checks failed\033[0m\n'
exit $FAILED
