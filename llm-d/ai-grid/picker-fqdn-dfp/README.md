# Picker-driven real FQDN forwarding (DNS + SNI) — Istio DFP + host_rewrite_header

The one path that lets the praxis **picker** choose an **FQDN** upstream and have the
gateway forward to it with real **DNS resolution** and correct **TLS SNI**. Distinct
from the EPP/ORIGINAL_DST path (which is IP-only). Verified against primary sources
2026-09-07; NOT yet deployed (gated on proxy version — see below).

## Why the EPP path can't do this, and this can

The GAIE endpoint-picker path (agentgateway InferencePool, or Istio ORIGINAL_DST
reading `x-gateway-destination-endpoint`) treats the picked value as a literal
**ip:port** — agentgateway parses it as a numeric Rust `SocketAddr`; ORIGINAL_DST does
no DNS. So a picker-chosen FQDN is rejected/unresolvable there. (See
`../agentgateway-picker/README.md` for that dead-end, empirically confirmed.)

This path is a different mechanism: the picker sets a **header**, and Envoy's
`dynamic_forward_proxy` (DFP) rewrites its **DNS-lookup host** from that header, then
resolves it and originates TLS with auto-SNI to the resolved host.

## Mechanism (Envoy, verified)

1. praxis picker (ext_proc, picker_mode) sets `x-chosen-host: <fqdn>:<port>` at the
   request-headers phase.
2. Route `typed_per_filter_config` -> DFP `PerRouteConfig.host_rewrite_header:
   x-chosen-host` — *"before DNS lookup, the host header will be swapped with the value
   of this header"* (Envoy DFP proto).
3. The DFP **filter** and the DFP **cluster** (`envoy.clusters.dynamic_forward_proxy`)
   resolve it. They MUST share ONE `dns_cache_config` (identical `name`) — a mismatch is
   the usual non-crash misconfig.
4. The cluster's TLS `transport_socket` gives **auto_sni + auto_san_validation** to the
   resolved host (on by default for DFP clusters), so SNI tracks the rewritten host.
5. Filter order: **ext_proc -> dynamic_forward_proxy -> router** (DFP reads the header
   at lookup time, so ext_proc must precede it).

Sources: Envoy `dynamic_forward_proxy` filter + cluster proto/docs (host_rewrite_header,
shared dns_cache, auto_sni); GAIE proposal-004.

## !!! GATED ON PROXY VERSION — CVE-2025-54588 !!!

The exact pattern here (Host modified between the DFP and Router filters) is the trigger
for **CVE-2025-54588**, a use-after-free in the DFP DNS cache that crashes Envoy.
- **Affected:** Envoy 1.34.0–1.34.4 and 1.35.0. **Fixed:** 1.34.5 and 1.35.1.
- **dagobah gateway proxy = Envoy 1.34.2-dev** (RHOAI/OSSM `istio-proxyv2-rhel9`,
  server_info `1.34.2-dev/3.1.0/RELEASE`) -> **AFFECTED.** This is what our earlier
  "DFP segfaulted Envoy on Istio" actually was. Not an architectural wall — a named,
  fixed bug.
- **To run here you MUST either:**
  - apply `00-cve-mitigation-flag.yaml` (sets
    `envoy.reloadable_features.dfp_cluster_resolves_hosts=false`), OR
  - run a patched proxy (Envoy >= 1.34.5 / 1.35.1 — whatever the next OSSM ships; the
    proxy is RHOAI's service-mesh image, so a bump is not a free pin).

## Apply order (when you actually need external picker-routed FQDN)

```bash
CTX=default/api-dagobah-hexfusion-local:6443/kube:admin
oc --context $CTX apply -f 00-cve-mitigation-flag.yaml   # REQUIRED on 1.34.2-dev
oc --context $CTX apply -f 10-dfp-host-rewrite.yaml        # edit workloadSelector + route match first
# praxis picker config: emit x-chosen-host = the chosen fqdn:port (route_header: x-chosen-host)
```

## Scope — ADDITIVE, not on the current migration path

- The `maas-default-gateway` cutover routes to **in-cluster site IPs** (site-a/b workload
  svcs, site-d ExternalName 192.168.1.230) via ORIGINAL_DST/header-route — **no FQDN, no
  DFP.** That path is proven and needs none of this.
- External providers (`claude-haiku -> anthropic`) already work via a **static**
  `x-ipp-selected-provider=anthropic` HTTPRoute. picker-ROUTED anthropic (the picker
  choosing anthropic per-request with DNS+SNI) is a **refinement**, not a requirement —
  this is the path for that day.
- So: banked as a ready recipe. Not deployed. Revisit when picker-driven external-FQDN
  routing is actually wanted.

## Second mechanism (not usable here, for the record)

agentgateway v1.5 dynamic-backend **CEL target expression** also reads ext_proc output
(`Dynamic(_, Expression)`, "evaluated against the request with ext_proc/extAuthz dynamic
metadata already attached") and could pick an FQDN target. But at v1.5.0 it's **Rust/
standalone-config only** (the k8s `DynamicForwardProxy` CRD message is empty) and can't
combine with GAIE inferenceRouting — so it's unreachable through the CRDs we deploy with.
Istio DFP (above) is the usable path.
