package main

import "fmt"

func main() {
	fmt.Println("sketch: run the scenarios with `go test -v ./...`")
	fmt.Println("  TestActiveActive   plain HTTP round-robin reaches every replica")
	fmt.Println("  TestDivergence     replicas herd without decision broadcast")
	fmt.Println("  TestMetadataPath   the decider decides without ever seeing the body")
}
