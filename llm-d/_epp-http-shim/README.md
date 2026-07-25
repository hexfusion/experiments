# epp-http-shim

A second front door for EPP: accept a routing call over plain HTTP and drive the
existing ext_proc entry point in process. No changes to EPP.

These files belong at `pkg/epp/handlers/httpshim/` in `llm-d-router`. They live
here under a leading underscore so the Go tool ignores them, since they import
`llm-d-router` and this module does not depend on it.

## Why it works

`StreamingServer.Process` takes an `ExternalProcessor_ProcessServer`. That is
eight methods, and six of them carry no meaning outside gRPC. Satisfying it with
a pair of channels turns the protobuf types into an internal calling convention,
so an HTTP handler can drive the real phase logic, the real director, the real
metrics and the real tracing.

Existing ext_proc callers are untouched. Both front doors run identical
scheduling because there is only one implementation.

## Two things building it surfaced

**EPP resolves parsers by path suffix.** A shim serving a route of its own gets
`no parser registered matching path suffix for: /v1/schedule`. So the shim is a
passthrough `http.Handler` with no route of its own: whatever path arrives is the
path EPP sees. Callers send the request they were already going to send and get a
decision instead of a completion. `TestPathIsPassedThrough` pins this.

**The drain has to run to completion.** Breaking out of the response loop on the
first useful message leaves `Process` blocked writing to a full channel, leaking
one goroutine per request. The input is therefore fully written and closed before
anything is read back, so `Process` always reaches EOF and always returns.
`TestConcurrentRequests` checks goroutine count across 300 concurrent requests.

## Running

From a `llm-d-router` checkout with the files in place:

```
GOTOOLCHAIN=auto go test -race -count=3 ./pkg/epp/handlers/httpshim/...
```

Verified against llm-d-router HEAD. Three tests pass, clean under `-race`.

## Not covered

The response phase. This drives the request phase only and returns a decision.
Usage accounting over the HTTP door needs either the caller to report back or the
caller to stay on the response path.

Metadata as headers. EPP still parses the body to find the model and prompt, so
the published-attributes carrier in ROUTABLE-DECIDER.md needs a change to EPP's
parsing that this does not make.
