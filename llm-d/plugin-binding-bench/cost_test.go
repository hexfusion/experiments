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

// BenchmarkParseOnly is unmarshal plus tokenization and block-key derivation,
// without the data producer.
func BenchmarkParseOnly(b *testing.B) {
	body := benchBody(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := parseBody(body); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExtract is the whole extraction stage: parse plus the prefix data
// producer. Under this design both belong to the shim.
func BenchmarkExtract(b *testing.B) {
	body := benchBody(b)
	if err := SetupExtraction(body, 50, 3, 50); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseOnce(body); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProducerByCacheDepth shows the axis that varies per request: the
// producer breaks at the first uncached block.
func BenchmarkProducerByCacheDepth(b *testing.B) {
	body := benchBody(b)
	for _, pct := range []int{0, 25, 50, 100} {
		b.Run("depth"+itoa(pct)+"pct", func(b *testing.B) {
			if err := SetupExtraction(body, pct, 3, 50); err != nil {
				b.Fatal(err)
			}
			m, err := parseBody(body)
			if err != nil {
				b.Fatal(err)
			}
			ix := globalExtractor.indexer
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ix.MatchLongestPrefix(m.BlockKeys)
			}
		})
	}
}

// BenchmarkScore is the plugin's own work, which a Rust shim would not change
// because the plugin is the thing being hosted.
func BenchmarkScore(b *testing.B) {
	body := benchBody(b)
	if err := SetupExtraction(body, 50, 3, 50); err != nil {
		b.Fatal(err)
	}
	m, err := ParseOnce(body)
	if err != nil {
		b.Fatal(err)
	}
	p := NewScorer("s", 50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Decide(m)
	}
}

// BenchmarkFanOutNative is parse plus three hosted plugins, the full shim path.
func BenchmarkFanOutNative(b *testing.B) {
	body := benchBody(b)
	if err := SetupExtraction(body, 50, 3, 50); err != nil {
		b.Fatal(err)
	}
	p := NewScorer("s", 50)
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
