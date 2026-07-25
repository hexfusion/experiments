.PHONY: bench sweep vet

vet:
	cd .. && go vet ./plugin-binding-bench/

bench:
	cd .. && go run ./plugin-binding-bench/ -endpoints=100 -repeat=3

sweep:
	@for e in 5 100 500; do \
		echo "########## endpoints=$$e ##########"; \
		(cd .. && go run ./plugin-binding-bench/ -endpoints=$$e -repeat=2); \
		echo; \
	done

envoy:
	cd .. && go run ./plugin-binding-bench/ -envoy -turns=20 -concurrency=8 -sessions=16 -endpoints=50

envoy-load:
	cd .. && go run ./plugin-binding-bench/ -envoy -turns=20 -concurrency=32 -sessions=64 -endpoints=50
