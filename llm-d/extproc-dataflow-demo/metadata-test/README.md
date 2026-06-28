# metadata-test: can a single pre-EPP filter read the EPP's pick in its body phase?

**Answer: no, and it is not a config issue.** This settles whether the IPP could be a *single*
ext_proc filter (do model selection in its header phase, read the EPP's pick from dynamic metadata in
its body phase, inject auth) instead of two filter positions bracketing the EPP.

## Setup

Chain: `ipp` (pre-EPP, `request_body_mode: BUFFERED`, `forwarding_namespaces: [envoy.lb, test.pick]`)
→ `epp` (sets `envoy.lb` + `test.pick` dynamic metadata) → `probe` (post-EPP, **same** forwarding as
`ipp`, the control) → echo.

## Result (one request)

```
[epp]       set metadata envoy.lb.pick="azure..."  test.pick.value="azure..."
[probe/hdr] (post-EPP)  ns=[envoy.lb test.pick]  envoy.lb.pick="azure..."   CONTROL: got it
[ipp/hdr]   (pre-EPP)   ns=[]                                                before EPP, empty (expected)
[ipp/body]  (post-EPP)  ns=[]                                                THE ANSWER: empty
```

## Why this is conclusive

- The **probe** has identical `forwarding_namespaces` and sits after the EPP; it receives both
  namespaces. So the metadata is set, saved, and forwardable. **Rules out a config issue.**
- The IPP's body phase **fired** (the `[ipp/body]` line printed, so `BUFFERED` body mode works) but its
  `metadata_context` is **empty**. The forwarded context reflects the filter's own position, not a
  later filter's (the EPP's) mutation.

## Conclusion

A single pre-EPP ext_proc filter cannot observe the EPP's pick in its body phase by **either** channel:
- header channel: `HttpBody` (the body `ProcessingRequest`) carries no headers, so a later filter's
  header mutation is invisible.
- metadata channel: the forwarded `metadata_context` does not carry metadata a later filter set
  (measured here, with a control).

Therefore the IPP's post-pick work needs its own filter position **after** the EPP. The IPP is one
service (one interceptor, before/after advice), hooked at two positions, correlated by `x-request-id`.
Run: `./run.sh` (needs Go + podman; Envoy `v1.31`).
