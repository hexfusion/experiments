#!/usr/bin/env bash
# Emits the site-d workload manifests. BACKEND selects the model server.
set -euo pipefail
BACKEND="${BACKEND:-vllm-cpu}"
MODEL="${MODEL:-Qwen/Qwen3-0.6B}"

case "$BACKEND" in
  vllm-cpu)
    IMAGE="docker.io/vllm/vllm-openai-cpu:v0.21.0"
    # vLLM's CPU backend sizes its KV cache from *node* memory and defaults to
    # 92% of it, so it refuses to start on a busy machine regardless of the
    # container limit. A 0.6B model needs a fraction of that.
    ARGS='["--model","MODEL_NAME","--port","8000","--max-model-len","4096","--dtype","bfloat16","--gpu-memory-utilization","0.08"]'
    ;;
  sim)
    IMAGE="ghcr.io/llm-d/llm-d-inference-sim:v0.10.2"
    ARGS='["--model","MODEL_NAME","--port","8000"]'
    ;;
  *) echo "BACKEND must be vllm-cpu or sim" >&2; exit 2 ;;
esac
ARGS="${ARGS/MODEL_NAME/$MODEL}"

cat <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: site-d
  labels:
    grid.internal/site: site-d
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: model-cache
  namespace: site-d
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 20Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: model
  namespace: site-d
spec:
  replicas: 1
  # One replica holding one model. A rolling update keeps the old pod alive
  # until the new one is ready, so a bad config leaves two pods competing
  # and the broken one keeps being recreated.
  strategy:
    type: Recreate
  selector:
    matchLabels: { app: model }
  template:
    metadata:
      labels:
        app: model
        llm-d.ai/inferenceServing: "true"
    spec:
      containers:
        - name: server
          image: ${IMAGE}
          # The node cannot reach a registry on this host, so the image is
          # loaded in from the host store. Pulling would hang.
          imagePullPolicy: IfNotPresent
          args: ${ARGS}
          # Unauthenticated Hub requests are rate limited and slower. The
          # secret is optional so the manifest applies without one.
          env:
            - name: HF_TOKEN
              valueFrom:
                secretKeyRef:
                  name: hf-token
                  key: HF_TOKEN
                  optional: true
          volumeMounts:
            - { name: model-cache, mountPath: /root/.cache/huggingface }
          ports:
            - { containerPort: 8000, name: http }
          readinessProbe:
            httpGet: { path: /health, port: 8000 }
            initialDelaySeconds: 20
            periodSeconds: 10
            failureThreshold: 120
          resources:
            requests: { cpu: "2", memory: 6Gi }
            limits: { cpu: "8", memory: 16Gi }
      # The model cache outlives the pod. Weights are the slow part of a
      # cold start on a constrained connection, and without this every
      # restart downloads them again.
      volumes:
        - name: model-cache
          persistentVolumeClaim:
            claimName: model-cache
---
apiVersion: v1
kind: Service
metadata:
  name: model
  namespace: site-d
spec:
  type: NodePort
  selector: { app: model }
  ports:
    - name: http
      port: 8000
      targetPort: 8000
      nodePort: 30080
YAML
