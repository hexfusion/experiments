package parser

import (
	"reflect"
	"testing"
)

// InferenceInput is bounded by design (Model/Stream/Prompt + the 3 kv.Store
// methods). Widening it is a deliberate design change; this test fails CI so
// the change cannot drift through unnoticed. If you are updating the count,
// re-read the 3-point test in 08b-interface-design-external.md first.
func TestInferenceInputBoundedSurface(t *testing.T) {
	const want = 6
	got := reflect.TypeFor[InferenceInput]().NumMethod()
	if got != want {
		t.Fatalf("InferenceInput has %d methods, want %d", got, want)
	}
}

// PromptStructured is a capability interface: single method by design.
// Adding a second method conflates concerns; introduce a new capability
// interface instead.
func TestPromptStructuredBoundedSurface(t *testing.T) {
	const want = 1
	got := reflect.TypeFor[PromptStructured]().NumMethod()
	if got != want {
		t.Fatalf("PromptStructured has %d methods, want %d", got, want)
	}
}
