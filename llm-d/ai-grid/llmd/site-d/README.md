# factory (site-d) model server

Applied by hand, not by Argo CD: `kubectl apply -k .` against the site-d k3s cluster, namespace llm-d. The router is a Helm release (see the comment in kustomization.yaml). Nothing here holds a secret; the HF token is an optional secretKeyRef.

The engine caps concurrency at 64 sequences (`--max-num-seqs=64`) so the grid's maxRunning for factory matches it. With TP=2 the pod holds both T4s, so the Deployment uses Recreate: a change restarts the model server, with a 5 to 10 minute reload.
