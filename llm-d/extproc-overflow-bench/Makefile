# One gateway at a time: make up GW=istio ... runs ... make down GW=istio, then next.
NS          ?= extproc-bench
GW          ?=
BASE_URL    ?= https://inference-gateway.$(NS)
ADMIN_URL   ?= http://localhost:19000
CLUSTER     ?=                 # ext_proc cluster name substring (admin /clusters)
GW_SELECTOR ?=                 # label selector for gateway pods
GIE_CHART   ?= oci://registry.k8s.io/gateway-api-inference-extension/charts/inferencepool
GIE_VERSION ?= v1.0.1          # pin to cluster GIE version
# EPP image built from PR #1603 branch (needed until merged) — set to your build:
EPP_HUB     ?= quay.io/sbatsche
EPP_NAME    ?= llm-d-router
EPP_TAG     ?= grpc-stream-metrics
# provider.name per gateway — verify against GIE chart values for your version:
PROVIDER_istio         = istio
PROVIDER_envoy-gateway = none
PROVIDER_kgateway      = kgateway
SIM_REPLICAS ?= 20             # scale so the backend never bottlenecks the ramp
PARALLELISM  ?= 4              # k6-operator shards (own pod IPs) for the big ramp
STAGES_BIG   ?= 2000,4000,10000,50000
RAMP_SECS    ?= 400            # scraper window covering one a1 ramp

# EPP_METRICS=1 → run the PR #1603 branch image with --enable-grpc-stream-metrics
# (EPP-side L_held gauge). Default: stock EPP, Envoy /stats is the primary signal.
ifdef EPP_METRICS
EPP_HELM_ARGS = --set inferenceExtension.image.hub=$(EPP_HUB) \
  --set inferenceExtension.image.name=$(EPP_NAME) \
  --set inferenceExtension.image.tag=$(EPP_TAG) \
  --set inferenceExtension.flags.enable-grpc-stream-metrics=true
PATCH_CMD = $(MAKE) patch-epp-flag
else
PATCH_CMD = true
endif

guard-GW:
	@test -n "$(GW)" || { echo "set GW=istio|envoy-gateway|kgateway"; exit 1; }

up: guard-GW
	kubectl apply -f common/namespace.yaml -f common/sim.yaml
	@[ "$(GW)" = istio ] && kubectl apply -f common/peerauth.yaml || true
	kubectl apply -f gateways/$(GW)/gateway.yaml
	helm upgrade --install epp $(GIE_CHART) --version $(GIE_VERSION) -n $(NS) \
	  --set provider.name=$(PROVIDER_$(GW)) \
	  --set inferencePool.targetPorts[0].number=8000 \
	  --set inferencePool.modelServers.matchLabels.app=sim \
	  $(EPP_HELM_ARGS)
	kubectl apply -f gateways/$(GW)/knobs.yaml
	@[ "$(GW)" = istio ] && kubectl apply -f gateways/$(GW)/procmode.yaml || true
	@$(PATCH_CMD)
	kubectl apply -f dashboards/podmonitor.yaml

# Chart renders flags as `--flag value` (two args) which breaks pflag bools; force
# the one-arg `=` form so --enable-grpc-stream-metrics actually takes effect.
patch-epp-flag:
	kubectl -n $(NS) patch deploy epp --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-grpc-stream-metrics=true"}]' || true

# Scrapers (run in two terminals, or & them). Need ADMIN_URL port-forward + CLUSTER/GW_SELECTOR.
stats:
	CLUSTER=$(CLUSTER) ADMIN_URL=$(ADMIN_URL) scripts/scrape-envoy-stats.sh 1200 & \
	NS=$(NS) GW_SELECTOR=$(GW_SELECTOR) scripts/scrape-pod-resources.sh 1200

scale: ; kubectl -n $(NS) scale deploy/sim --replicas=$(SIM_REPLICAS)

# Distributed big ramp (2k→50k) via k6-operator. Run `make scale` first.
bigramp:
	kubectl -n $(NS) create configmap k6-overflow-ramp \
	  --from-file=overflow-ramp.js=load/k6/overflow-ramp.js \
	  --dry-run=client -o yaml | kubectl apply -f -
	NS=$(NS) PARALLELISM=$(PARALLELISM) BASE_URL=$(BASE_URL) STAGES_BIG=$(STAGES_BIG) \
	  envsubst < load/k6/testrun.yaml | kubectl apply -f -
bigramp-clean: ; -kubectl -n $(NS) delete testrun extproc-bigramp; -kubectl -n $(NS) delete cm k6-overflow-ramp

# Single-box A1 (breaker knee ~1024xW): fine ramp, no distributed runner needed.
a1: ; k6 run --out csv=results/k6-ts-$(GW)-a1.csv -e STAGES=512,1024,2048,4096 -e BASE_URL=$(BASE_URL) -e TAG=$(GW)-a1 load/k6/overflow-ramp.js

# One-shot A1: scale sim, scrape during the ramp, run a1, summarize. Commands are
# inlined (no sub-make) so `make -n a1-run` stays a true dry run.
# Prereqs: `make up GW=...` done; Envoy admin reachable at ADMIN_URL (port-forward);
# CLUSTER + GW_SELECTOR set.
a1-run: guard-GW
	@test -n "$(CLUSTER)" -a -n "$(GW_SELECTOR)" || { echo "set CLUSTER=<ext_proc cluster> GW_SELECTOR=<gw pod label>"; exit 1; }
	kubectl -n $(NS) scale deploy/sim --replicas=$(SIM_REPLICAS)
	bash -c 'CLUSTER=$(CLUSTER) ADMIN_URL=$(ADMIN_URL) scripts/scrape-envoy-stats.sh $(RAMP_SECS) & e=$$!; \
	  NS=$(NS) GW_SELECTOR=$(GW_SELECTOR) scripts/scrape-pod-resources.sh $(RAMP_SECS) & p=$$!; \
	  k6 run --out csv=results/k6-ts-$(GW)-a1.csv -e STAGES=512,1024,2048,4096 -e BASE_URL=$(BASE_URL) -e TAG=$(GW)-a1 load/k6/overflow-ramp.js; \
	  wait $$e $$p'
	python3 scripts/summarize.py results
a2: ; @echo ">> first: set R/K/S=1000000 in gateways/$(GW)/knobs.yaml && kubectl apply"; \
	k6 run --out csv=results/k6-ts-$(GW)-a2.csv -e BASE_URL=$(BASE_URL) -e TAG=$(GW)-a2 load/k6/overflow-ramp.js
b: ; @echo ">> first: set response_body_mode=NONE (istio: gateways/istio/procmode.yaml) && apply"; \
	k6 run --out csv=results/k6-ts-$(GW)-b.csv -e BASE_URL=$(BASE_URL) -e TAG=$(GW)-b load/k6/overflow-ramp.js
ttft: ; BASE_URL=$(BASE_URL) load/guidellm/ttft-sweep.sh
summarize: ; python3 scripts/summarize.py results

down: guard-GW
	-helm uninstall epp -n $(NS)
	-kubectl delete -f gateways/$(GW)/ --ignore-not-found
	@echo "kept ns+sim for next gateway; 'make purge' to remove all"
purge: ; -kubectl delete -f common/

.PHONY: guard-GW up patch-epp-flag scale bigramp bigramp-clean stats a1 a1-run a2 b ttft summarize down purge
