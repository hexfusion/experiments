#!/usr/bin/env bash
# k6 load test: llm-d (hub EPP) vs round-robin baseline, tagged per cluster, one run per arm.
#   MODE=load [LOADED=1]  per-cluster latency + load-shed distribution (heats spoke-a)
#   MODE=affinity         session stickiness
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KIND_EXPERIMENTAL_PROVIDER=podman
K()  { kubectl --context kind-aig-hub    -n llm-d "$@"; }
KA() { kubectl --context kind-aig-spoke-a -n llm-d "$@"; }

MODE="${MODE:-load}"; RATE="${RATE:-8}"; DURATION="${DURATION:-90s}"; MODEL="${MODEL:-model-a}"
OUTPUT_TOKENS="${OUTPUT_TOKENS:-64}"; MAXVUS="${MAXVUS:-400}"; VUS="${VUS:-8}"
LOADED="${LOADED:-0}"; LOAD_CONC="${LOAD_CONC:-48}"
GWA="${GWA:-$(KA get gateway spoke -o jsonpath='{.status.addresses[0].value}' 2>/dev/null)}"
IMAGE="${IMAGE:-grafana/k6:0.49.0}"
DATA="$HERE/data"; mkdir -p "$DATA"

podman image exists "$IMAGE" || podman pull -q "$IMAGE"
kind load docker-image "$IMAGE" --name aig-hub >/dev/null 2>&1 || true
K create configmap k6-script --from-file=test.js="$HERE/test.js" --dry-run=client -o yaml | K apply -f - >/dev/null

gw() { K get gateway "$1" -o jsonpath='{.status.addresses[0].value}'; }
LLMD_TARGET="http://$(gw hub):8080"
BASELINE_TARGET="http://$(gw hub-rr):8080"

heat() { [ "$LOADED" = 1 ] || return 0; echo "heating spoke-a"
  KA delete pod spokeload --force --grace-period=0 >/dev/null 2>&1 || true
  KA run spokeload --image=python:3.12-slim --restart=Never --command -- python3 -c "
import urllib.request,json,threading,time,random
def w():
  t=time.time()
  while time.time()-t<600:
    # varied prompt per request so the spoke prefix-cache-scorer does NOT pin all load to one pod
    b=json.dumps({'model':'model-a','messages':[{'role':'user','content':'load %f'%random.random()}],'max_tokens':256}).encode()
    try: urllib.request.urlopen(urllib.request.Request('http://${GWA}:8080/v1/chat/completions',data=b,headers={'content-type':'application/json'}),timeout=30).read()
    except: pass
ts=[threading.Thread(target=w) for _ in range(${LOAD_CONC})];[t.start() for t in ts];[t.join() for t in ts]" >/dev/null 2>&1
  sleep 12; }
unheat() { [ "$LOADED" = 1 ] && KA delete pod spokeload --force --grace-period=0 >/dev/null 2>&1 || true; }

run() {
  local label="$1" target="$2"
  echo "==> $label  $target  mode=$MODE rate=$RATE dur=$DURATION loaded=$LOADED"
  K delete job "k6-$label" --ignore-not-found >/dev/null 2>&1
  K apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata: { name: k6-$label, namespace: llm-d }
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: k6
          image: $IMAGE
          command: ["k6","run","/scripts/test.js"]
          env:
            - { name: TARGET,        value: "$target" }
            - { name: MODE,          value: "$MODE" }
            - { name: RATE,          value: "$RATE" }
            - { name: DURATION,      value: "$DURATION" }
            - { name: MODEL,         value: "$MODEL" }
            - { name: OUTPUT_TOKENS, value: "$OUTPUT_TOKENS" }
            - { name: MAXVUS,        value: "$MAXVUS" }
            - { name: VUS,           value: "$VUS" }
          volumeMounts: [ { name: script, mountPath: /scripts } ]
      volumes: [ { name: script, configMap: { name: k6-script } } ]
EOF
  K wait --for=condition=complete "job/k6-$label" --timeout=300s >/dev/null 2>&1 || echo "   (k6-$label not complete)"
  K logs "job/k6-$label" 2>/dev/null | sed -n 's/^K6SUMMARY //p' | tail -1 > "$DATA/$label.json"
  [ -s "$DATA/$label.json" ] && echo "   $label ok" || echo "   !! no summary for $label"
}

heat
run llm-d    "$LLMD_TARGET"
run baseline "$BASELINE_TARGET"
unheat
python3 "$HERE/plot.py" "$DATA" "$HERE/graphs"
