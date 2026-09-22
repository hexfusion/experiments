# ai-grid v2

Raw Envoy + `praxis-extproc` ext_proc callout: residency via `region_claim`.
Namespace `ai-grid-v2`. Self-contained (mock backends, in-cluster).

```
kubectl apply -k .
./mint-region-tokens.py
./verify-geo.sh --mode v2 --url https://<route>
```

Image `praxis-extproc:ipp-v3.8`.
