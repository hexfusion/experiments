# Grid frontdoor (grid-gateway) with FQDN support — APPLIED LIVE

The live grid-gateway frontdoor config after the FQDN cutover (2026-09-07). Fixes the
mono-routing-to-site_a bug and gives the frontdoor real FQDN + live-load routing.

- **extproc.yaml** — praxis chain on **ipp-v2**: json_body_field(model->X-Model) -> policy(JWT auth)
  -> rate_limit(per_identity, meter:tokens) -> token_count -> intelligent_route
  (route_header x-gateway-destination-endpoint, live-load signals, candidates = site-a/site-b
  workload-svc **FQDNs** with trailing dot :8000). emit_decision_header dropped (deny_unknown_fields
  on ipp-v2). policy.yaml = the JWT auth (unchanged).
- **envoy.yaml** — ext_proc(BUFFERED, metadata_options envoy.lb) -> header_mutation
  (%DYNAMIC_METADATA envoy.lb/x-gateway-destination-endpoint -> x-chosen-host) ->
  dynamic_forward_proxy(host_rewrite_header x-chosen-host) -> router; dfp_cluster is a
  dynamic_forward_proxy cluster with TLS (auto-SNI, ACCEPT_UNTRUSTED) for the site vLLMs.

Model comes from the JSON body (json_body_field); BUFFERED ext_proc holds the chain until the
body-phase decision, so the envoy.lb metadata is set before header_mutation+DFP. Validated:
alice token -> 200 real Qwen3 inference, intelligent_route picked site-b by live load, DFP
resolved the site-b FQDN over TLS, site-b vLLM served it. Pre-change backup:
../../grid-frontdoor-backup/ (revert source). This is the frontdoor form of ../ (picker-fqdn).
