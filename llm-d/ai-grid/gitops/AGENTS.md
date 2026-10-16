# AI Grid GitOps maintenance

- Work on main; direct signed pushes are authorized for this experiments repository.
- Keep site identity and placement in sites/, model definitions in models/, and reusable
  templates in charts/. Current observability configuration belongs in observability/.
- Update payload/customizations.yaml for every custom or planned carry, with exact source,
  deployment state, merge state and retirement action. Update payload/images.yaml and
  docs/image-inventory.md when image pins or build recipes change.
- Keep payload/selection.yaml consistent with site/model inputs. Pin deployed images by
  digest and chart sources by full commit. Distinguish source inspection, signature checks
  and runtime acceptance. Pending sources are not deployed payloads.
- Paths in payload inventories are relative to this directory. Runnable checks belong in
  tests/, recorded results in evidence/, and custom source/build recipes in payload/patches/.
- Preserve unrelated work. A directory migration must preserve rendered resource identity
  and update active Argo source paths before retiring the old paths.
