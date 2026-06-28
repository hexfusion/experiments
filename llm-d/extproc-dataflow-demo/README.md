# ext_proc post-pick demo

## Summary

Envoy ext_proc runs post-pick logic (per-destination auth, and translation when needed) after
a routing decision.

The IPP brackets the EPP with two hooks (one IPP process):

```
client: {"model":"gpt-4","messages":[...]}            (real OpenAI body)
  IPP pre  (BUFFERED)      parse body -> model, stash entitlement, set x-gateway-model-name
  EPP                      route on the model -> pick a destination
  IPP post (header-only)   inject Authorization: Bearer <key>          [auth, always, no body]
           (+ BUFFERED)    translate body to the destination's API     [only if the API differs]
```

Two paths, shown in one run:
- **`gpt-4` -> openai (OpenAI-compatible):** auth only, header mutation, **body untouched, no buffer**.
- **`claude-3` -> anthropic (different API):** auth **+** body translated; `mode_override` flips the
  post hook into buffering **only** for this case.

So you only pay the post-pick body buffer when you actually translate. Auth is a header mutation; the
entitlement is recovered server-side by `x-request-id` and never travels as a header.

- The model is read from the **body** (IPP-pre, BUFFERED), like BBR — not a header shortcut.
- One gRPC stream per request **per filter** ([envoy#35317](https://github.com/envoyproxy/envoy/issues/35317));
  the IPP's two hooks (same process) correlate by `x-request-id`.
- Why two filter positions, not one: a pre-EPP filter can't see the pick by either channel. `metadata-test/`
  proves it (with a control).

## Run

```
./run.sh           # two real requests: gpt-4 (auth only) and claude-3 (auth + translate)
./stress.sh        # concurrent burst; asserts each response's auth + translation matches its request
./istio/run.sh     # same chain on Istio (RHAII's data plane) via EnvoyFilter; needs kind + istioctl
```
