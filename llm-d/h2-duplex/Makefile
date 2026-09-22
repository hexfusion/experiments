.PHONY: test run controls scale build

build:
	go build ./...

test:
	go test -v ./...

run:
	go run .

controls:
	@echo "########## http/1.1 ##########"
	@go run . -h1
	@echo
	@echo "########## upstream drains body before responding ##########"
	@go run . -no-duplex-upstream
	@echo
	@echo "########## first upstream body write deferred 300ms ##########"
	@go run . -delay-first-write=300ms

scale:
	@for kb in 32 256 1024; do \
		echo "--- chunk-kb=$$kb ---"; \
		go run . -chunk-kb=$$kb | grep -E "request body|high water|DUPLEX"; \
	done
