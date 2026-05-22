.PHONY: all build clean tidy verify
.PHONY: 01-basic 02-vllm-sim 03-kind-raw
.PHONY: 01-up 01-test 01-down
.PHONY: 02-up 02-test 02-down
.PHONY: 03-up 03-test 03-down
.PHONY: 04-up 04-test 04-down
.PHONY: praxis-gateway-up praxis-gateway-test praxis-gateway-down

GOFLAGS := -trimpath -ldflags='-s -w'
export CGO_ENABLED := 0

all: build

build: 01-basic 02-vllm-sim 03-kind-raw

01-basic:
	go build $(GOFLAGS) -o 01-basic/bbr-mock/bin/bbr-mock   ./01-basic/bbr-mock
	go build $(GOFLAGS) -o 01-basic/epp-mock/bin/epp-mock   ./01-basic/epp-mock
	go build $(GOFLAGS) -o 01-basic/vllm-mock/bin/vllm-mock ./01-basic/vllm-mock

02-vllm-sim:
	go build $(GOFLAGS) -o 02-vllm-sim/bbr-mock/bin/bbr-mock ./02-vllm-sim/bbr-mock
	go build $(GOFLAGS) -o 02-vllm-sim/epp-mock/bin/epp-mock ./02-vllm-sim/epp-mock

# 03 deploys the upstream llm-d EPP image on kind; we only need to build
# our own bbr-mock binary here.
03-kind-raw:
	go build $(GOFLAGS) -o 03-kind-raw/bbr-mock/bin/bbr-mock ./03-kind-raw/bbr-mock

01-up:   ; ./01-basic/scripts/up.sh
01-test: ; ./01-basic/scripts/test.sh
01-down: ; ./01-basic/scripts/down.sh

02-up:   ; ./02-vllm-sim/scripts/up.sh
02-test: ; ./02-vllm-sim/scripts/test.sh
02-down: ; ./02-vllm-sim/scripts/down.sh

03-up:   ; ./03-kind-raw/scripts/up.sh
03-test: ; ./03-kind-raw/scripts/test.sh
03-down: ; ./03-kind-raw/scripts/down.sh

04-up:   ; ./04-pd-t4/scripts/up.sh
04-test: ; ./04-pd-t4/scripts/test.sh
04-down: ; ./04-pd-t4/scripts/down.sh

# Off-series: praxis-gateway explores Praxis (Rust/Pingora) as a gateway
# replacement for Envoy + ext_proc. Same vllm-sim backend pool as 02.
praxis-gateway-up:   ; ./praxis-gateway/scripts/up.sh
praxis-gateway-test: ; ./praxis-gateway/scripts/test.sh
praxis-gateway-down: ; ./praxis-gateway/scripts/down.sh

tidy:
	go mod tidy

clean:
	rm -rf */*/bin

# Fast guardrail: compiles every binary, syntax-checks every script,
# dry-run-validates every manifest and compose file. No cluster needed.
verify:
	@echo "==> go build"
	@go build ./...
	@echo "==> shellcheck (syntax only)"
	@find . -path ./vendor -prune -o -name '*.sh' -print | xargs -r -n1 bash -n
	@echo "==> kubectl --dry-run on 03 manifests"
	@kubectl apply --dry-run=client -f 03-kind-raw/manifests/ >/dev/null
	@echo "==> podman compose config (01, 02, praxis-gateway)"
	@for f in 01-basic 02-vllm-sim praxis-gateway; do \
	  (cd $$f && podman compose config -q) || exit 1; \
	done
	@echo "OK"
