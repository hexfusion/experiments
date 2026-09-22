# Payload build

Reproducible builds of the demo images, pinned to git hashes, with the composing
PRs stamped into each image so provenance is the PR list and not a bare SHA.

## Why

The images the demo ran on carried only a git SHA or nothing, so you could not
tell which PRs were in them. Geo routing and rate limiting also live in different
builds, so no single image had both. This pipeline clones each component at a
pinned hash, stitches them into one Cargo workspace, and labels the result.

## Model

`manifest.toml` names components and images.

- A component is one repo at one immutable hash, with the PR numbers it carries
  and a local mirror used as a fast source. The build verifies the mirror holds
  that hash before using it.
- An image is a base `ai` component built through a generated Containerfile,
  optionally with a `policy` checkout patched in for a policy builtin that is not
  yet published (the quota plugin).

Two plugin systems compose here. `ai` filters auto-register at build time through
`server/build.rs`. PPE policy plugins (quota, identity/jwt) are builtins compiled
into the `praxis-policy` crates, which `ai` pulls from crates.io. An unpublished
policy builtin is stitched by patching `praxis-policy*` to the local checkout via
`[patch.crates-io]`, generated from the crates the lockfile actually uses.

## Use

```
./build.sh geo-quota      # assemble, build, label; prints the push command
```

The build does not push. Push is a separate manual step.

## geo-quota

`praxis-ai-gateway:geo-quota-v1` = `ai` match_claims geo routing (#1283) plus the
`policy` token-quota builtin (#116, #117). This is the image the v3 instance needs
to run geo routing and rate limiting together; the older `0.6.0-geo-v3` tag was
never built and carried no quota.
