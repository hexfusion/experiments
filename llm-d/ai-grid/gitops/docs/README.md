# Documentation

[Engineering handoff — 2026-10-16](handoff-20261016-enrollment.md) records the enrollment
API draft, validation results, saved work and next steps.

Start with [object ownership](object-ownership.md) for the admin/controller writer of
each installation resource and the certificate generation and lifecycle inventory.

Start with [operations and namespaces](operations.md), [gateway configuration](gateway-configuration.md),
[the outstanding payload diff](payload-diff.md), and [the topology](../topology.yaml).
[Authentication](auth-flow.md) describes trust boundaries. [ACM and GitOps](acm-gitops-flow.md)
describes lifecycle ownership. [Observability](../observability/README.md) covers metric collection.

The dated documents are lab observations at their recorded time. They are evidence, not
current installation recipes. Source pins and merge status live under `../payload/`;
current site inputs live under `../sites/`. [The writer handoff](docs-writer-handoff-20261016.md)
collects validated behavior and remaining qualification boundaries.
