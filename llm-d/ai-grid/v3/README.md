# ai-grid v3

Pure praxis gateway: identity + residency (`match_claims`) + token budget, in one
process. Namespace `ai-grid-v3`. Self-contained (mock backends, in-cluster).

```
kubectl apply -k .
./mint-region-tokens.py
./verify-geo.sh --mode v3 --url https://<route>
```

Image `praxis-ai-gateway:0.6.0-geo-v3` (combined load-datasource + match_claims build).
