# Branches → images in the demo

The custom images this demo runs, and the exact source each is built from.
Rebuild an image by checking out its branch and building; bump the tag in
`kustomization.yaml` (`images:`) to roll it out.

| image | source repo | branch | pin |
|-------|-------------|--------|-----|
| `quay.io/sbatsche/praxis-extproc:ai-grid-v8` | opendatahub-io/praxis-extproc | `ai-grid-extproc-demo` | (rebuild target; drops the kuadrant spike) |
| `quay.io/sbatsche/grid-operator:signals-7944cb4` | praxis-proxy/grid | `feat/routing-freshness` | — |
| `image-registry…/grid-system/praxis-ai:frontdoor` | praxis-proxy/ai | `feat/intelligent-route` | `15468623` |

## Dependency pins (compiled into the frontdoor / extproc)

These are the fork commits the Rust builds link against (git-rev, on the hexfusion forks):

| crate source | fork | commit |
|--------------|------|--------|
| praxis core  | hexfusion/praxis  | `452880eb` (feat/peer-spiffe-identity) |
| ai filters   | hexfusion/ai      | `15468623` (feat/intelligent-route) |
| policy (ppe) | hexfusion/policy  | `debc8b5`  (workload-claims) |
| pingora fork | hexfusion/pingora | `28acf22`  (feat/uri-sans, URI-SAN diff) |

wasm-shim/kuadrant is intentionally dropped (use the `ai-grid-extproc-demo` branch).
