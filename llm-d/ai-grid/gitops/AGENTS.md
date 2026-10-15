# AI Grid GitOps maintenance

- Update docs/image-inventory.md and image-inventory.yaml whenever image repositories,
  chart versions, source pins or custom payload patches change.
- Use immutable repository@sha256 references for selected images, including custom
  builds, init containers, hooks, tests and OLM artifacts. Version tags are discovery
  metadata, not pins. Document any tooling exception and its resolution before rollout.
- Keep final selections in payload.yaml consistent with values and embedded manifests.
  Preserve base-image provenance when adding or retiring a custom replacement.
- Distinguish registry/source-label inspection from signature verification and runtime
  testing. Unselected candidates are not a deployable payload.
- Shared templates live in bootstrap/ and apps/; lab configuration is values/lab.yaml.
- This experiments effort permits direct signed pushes to main. Preserve unrelated work.
