# Outstanding payload changes

Checked 2026-10-16. Exact source revisions, image digests and next actions are in
[customizations.yaml](../payload/customizations.yaml). The table distinguishes source
merge work from artifact publication and deployment.

| Change | Current state | What remains |
|---|---|---|
| ODH platform [#4186](https://github.com/opendatahub-io/opendatahub-operator/pull/4186) | PR source deployed through an ODH build | Merge and publish a consistent operator, manifests, bundle and catalog |
| ODH chart [#181](https://github.com/opendatahub-io/odh-gitops/pull/181) | Open; selected installation-chart source | Merge, package and qualify the chart |
| OMC [#966](https://github.com/opendatahub-io/odh-model-controller/pull/966) / KServe module | OMC merged and deployed; module still has a custom RBAC overlay | Publish a KServe module containing the merged RBAC |
| Praxis ExtProc processing modes | Custom image deployed; local patch has no matching PR identified | Submit the patch, publish ODH image, repeat unchanged-Envoy streaming tests |
| AI Gateway processing-mode configuration | Paired custom controller image deployed; no matching PR identified | Submit the paired configuration change and publish a compatible image |
| OMC credential-reader [#974](https://github.com/opendatahub-io/odh-model-controller/pull/974) | Open, signed implementation pushed; lab chart currently supplies the grant | Merge, publish, validate automatic grants and retire the lab grant |
| Grid prefix affinity [#391](https://github.com/praxis-proxy/grid/pull/391) | Required in the next custom gateway; not deployed | Include pinned `3fe4592`, merge, sync to ODH, build and qualify |
| Grid AI/Praxis bump [#392](https://github.com/praxis-proxy/grid/pull/392) | Merged at `a134b2a`; current gateway still older | Consume source in the next build with matching health probes |
| Grid health listener [#393](https://github.com/praxis-proxy/grid/pull/393) | Open | Qualify and merge chart/config migration to `admin.metrics_address` |
| Grid dependency checks [#394](https://github.com/praxis-proxy/grid/pull/394) | Open; build/CI change, no runtime behavior change | Merge after health-listener integration |
| Grid SiteInvite [#382](https://github.com/praxis-proxy/grid/pull/382), ODH sync [#35](https://github.com/opendatahub-io/praxis-ai-grid-operator/pull/35) | Both merged | Select and deploy matching enrollment image, CRD and chart; qualify lifecycle |
| Grid fresh-pressure routing [#386](https://github.com/praxis-proxy/grid/pull/386) | Merged, included in newer source | Publish and qualify the newer ODH payload |
| Grid build compatibility [#384](https://github.com/praxis-proxy/grid/pull/384) | Open, conditional and not selected | Carry only if the selected build needs it |
| Controller `GOMEMLIMIT=192MiB` | Manual deployment setting | Package through its manifest owner |
| Local Qwen CUDA driver selection | GitOps change already on main | Both engines started; protected serving acceptance is separate |

PR #391 keeps defaults of 16,384 entries and 600 seconds. It affects `grid_site_route`;
the current manually configured canary uses `intelligent_route`, so including the code
alone does not activate those settings in that route.

The standalone gateway test passed JSON/SSE URL-prefix cases. Generated provider routing
still needs qualification: at the inspected Grid source, endpoint derivation keeps host
and port but drops the path. Issue #248 closed with #392; that closure does not establish
end-to-end acceptance for the generated KServe publisher URLs.

No KServe readiness patch is selected. Its intended contract is awaiting maintainer
clarification. Envoy image/config changes are not part of this payload.
