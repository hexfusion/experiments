.PHONY: run test bench bench-stat clean

# Default bench count for bench-stat. Override: make bench-stat COUNT=20.
COUNT ?= 10

run:
	go run .

test:
	go test ./parser/

# Single-shot bench, refreshes benchmarks/results.txt.
bench:
	go test -bench=. -benchmem -benchtime=3s -run=^$$ ./benchmarks/ | tee benchmarks/results.txt

# Multi-run bench through benchstat for variance characterization.
# Writes raw output to benchmarks/results-multi.txt and a summary to
# benchmarks/results-stat.txt.
#
# Install benchstat first if missing:
#   go install golang.org/x/perf/cmd/benchstat@latest
bench-stat:
	@command -v benchstat >/dev/null || { \
		echo "benchstat not found. Install: go install golang.org/x/perf/cmd/benchstat@latest"; \
		exit 1; \
	}
	go test -bench=. -benchmem -benchtime=3s -count=$(COUNT) -run=^$$ ./benchmarks/ \
		| tee benchmarks/results-multi.txt
	benchstat benchmarks/results-multi.txt | tee benchmarks/results-stat.txt

clean:
	rm -f benchmarks/*.test benchmarks/results-multi.txt benchmarks/results-stat.txt
