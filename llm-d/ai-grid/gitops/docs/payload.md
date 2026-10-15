# Payload and custom patches

Prefer published ODH images directly. Tags, including static version tags, may help
find an artifact, but selected images must use immutable `repository@sha256:...`
references. Apply this to custom builds, init/helper containers, hooks/tests, and
bundle/catalog images as well. Resolve custom images after publishing; record tags
only as discovery metadata. Document any tooling exception, its reason and planned
resolution before rollout; an unresolved digest is not a deployment selection. Selecting
an image requires checking its source revision, not just its tag or publication date.

Use the maintained [image inventory](image-inventory.md) for registry/source mappings
and candidate digests. Update it in the same commit as every payload change.

See the [current carry assessment](carry-patches.md) for required changes, merged
requirements and integration gaps.

## Patch inventory

`../payload.yaml` records confirmed patches separately from undecided patches
and already-merged requirements. Open PR heads can change; updates require an
explicit revision update and rebuild/revalidation. Do not treat a merged PR as
present in an older ODH image. Check the image's revision against source ancestry.

For every selected artifact, record:

- Full pull reference and manifest/index digest; target architecture availability.
- Repository, base revision, ordered patch revisions, and resulting build revision.
- PR URLs, rationale, and the condition for retiring each patch.
- Build recipe and build/CI URL; signing/provenance verification where available.
- Render, compatibility, and lab acceptance results.

The platform image, bundle/CSV, embedded manifests, related-image environment and
catalog must describe the same selection. Swapping only a Deployment image is not
a complete payload. Keep MaaS controller/API/discovery, gateway payload processing
and Grid operator/runtime versions compatible with the pinned manifests.

Use a custom image only when no suitable ODH build includes the required change.
Build from the relevant downstream/ODH recipe, preserve downstream packaging, and
publish to the lab registry. Sign source commits with the configured verified key.
The registry digest identifies the binary; a source commit alone does not.

## Promotion gate

The initial inventory is intentionally incomplete. Do not install workload
Applications while decisions are pending or `artifacts` is empty. Promotion needs
an entry for every rendered image, registry verification, tested architecture,
and a restore point for current configuration and state.

For chart image overrides, prefer full `global.relatedImages` entries when supported.
Changing repository or tag alone can leave a default digest selected. Re-render and
inspect every resulting container and initContainer image. Grid's overlay-sync
binary has its own image; a general-purpose CLI image is not a substitute.

## Retiring a patch

Verify the upstream merge is present in the chosen ODH source revision, then verify
the published image was built from it. Replace the custom digest, rerun acceptance,
and record the replacement commit and result before removing the patch entry.
Never silently drop unmerged lab changes during an image refresh.
