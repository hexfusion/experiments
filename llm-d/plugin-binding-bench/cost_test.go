package main

import (
	"encoding/json"
	"testing"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// These split the shim's per-request CPU into its parts, so the question of
// whether a Rust shim would move the saturation point can be answered from the
// share each part takes rather than by assertion.

func benchBody(tb testing.TB) []byte {
	conv := DefaultConversation()
	conv.Turns = 20
	reqs := conv.Requests()
	return reqs[len(reqs)/2] // mid-conversation, close to the mean size
}

// BenchmarkJSONUnmarshal is encoding/json alone, no metadata extraction.
func BenchmarkJSONUnmarshal(b *testing.B) {
	body := benchBody(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req ChatRequest
		if err := jsonUnmarshal(body, &req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseOnce is unmarshal plus tokenization and block-key derivation,
// which is the whole shim-side parse.
func BenchmarkParseOnce(b *testing.B) {
	body := benchBody(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseOnce(body); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkScore is the plugin's own work, which a Rust shim would not change
// because the plugin is the thing being hosted.
func BenchmarkScore(b *testing.B) {
	body := benchBody(b)
	m, err := ParseOnce(body)
	if err != nil {
		b.Fatal(err)
	}
	p := NewScorer("s", 50, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Decide(m)
	}
}

// BenchmarkFanOutNative is parse plus three hosted plugins, the full shim path.
func BenchmarkFanOutNative(b *testing.B) {
	body := benchBody(b)
	p := NewScorer("s", 50, 64)
	hosted := []Binding{NewNativeBinding(p), NewNativeBinding(p), NewNativeBinding(p)}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FanOut(b.Context(), body, hosted); err != nil {
			b.Fatal(err)
		}
	}
}
