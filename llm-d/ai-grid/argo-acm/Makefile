# Render every Argo CD source in this directory from chart.env and the values files.
# The manifests are generated: never edit them, change the values and run `make render`.
#
#   make render                     # from the OCI chart in chart.env
#   make render CHART=./chart.tgz   # from a local package, before it is pushed
#   make check                      # fail if the committed manifests differ from a render

include chart.env

HELM ?= helm
YQ ?= yq
OUT ?= .
COMMON := values/common.yaml
SITES := $(patsubst sites/%/values.yaml,%,$(wildcard sites/*/values.yaml))
# A local chart has no version to select.
CHART_ARGS = $(CHART) $(if $(findstring oci://,$(CHART)),--version $(VERSION),) --skip-tests
# --api-versions for each API a target file lists, so capability checks render as on the cluster.
API_ARGS = $$($(YQ) -r '(.apiVersions // [])[] | "--api-versions " + .' $(1))

.PHONY: render check

render:
	@set -e; for site in $(SITES); do \
		dir=$(OUT)/sites/$$site/manifests; rm -rf $$dir; mkdir -p $$dir; \
		$(HELM) template grid-$$site $(CHART_ARGS) $(call API_ARGS,sites/$$site/site.yaml) --namespace grid \
			-f $(COMMON) -f sites/$$site/values.yaml --output-dir $$dir >/dev/null; \
		echo "rendered sites/$$site"; \
	done
	@set -e; dir=$(OUT)/hub/enrollment/manifests; rm -rf $$dir; mkdir -p $$dir; \
		$(HELM) template grid-enrollment $(CHART_ARGS) $(call API_ARGS,hub/enrollment/target.yaml) --namespace grid-enrollment \
			-f hub/enrollment/values.yaml --output-dir $$dir >/dev/null; \
		echo "rendered hub/enrollment"

check:
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf $$tmp' EXIT; \
		$(MAKE) --no-print-directory render OUT=$$tmp >/dev/null; \
		status=0; \
		for dir in $(SITES:%=sites/%/manifests) hub/enrollment/manifests; do \
			diff -r $$dir $$tmp/$$dir || status=1; \
		done; \
		if [ $$status -ne 0 ]; then echo "manifests differ from the render: run make render"; fi; \
		exit $$status
