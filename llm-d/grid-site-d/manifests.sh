#!/usr/bin/env bash
# Emits the site-d workload manifests. BACKEND selects the model server.
set -euo pipefail
BACKEND="${BACKEND:-vllm-cpu}"
MODEL="${MODEL:-Qwen/Qwen3-0.6B}"

case "$BACKEND" in
  vllm-cpu)
    IMAGE="vllm/vllm-openai-cpu:v0.21.0"
    ARGS='["--model","MODEL_NAME","--port","8000","--max-model-len","4096","--dtype","bfloat16"]'
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
apiVersion: apps/v1
kind: Deployment
metadata:
  name: model
  namespace: site-d
spec:
  replicas: 1
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
          args: ${ARGS}
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
