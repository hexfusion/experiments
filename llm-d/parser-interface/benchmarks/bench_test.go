// Package benchmarks exercises the parser framework end-to-end so each
// benchmark tells a clear cost-story. Run with:
//
//	go test -bench=. -benchmem ./benchmarks/
package benchmarks

import (
	"context"
	"testing"

	"prototype/kv"
	"prototype/parser"
	"prototype/producer/cost"
	"prototype/producer/estimator"
	"prototype/vendors/anthropic"
	"prototype/vendors/anthropic_proj"
	"prototype/vendors/openai"
	"prototype/vendors/passthrough"
)

var (
	anthropicBody = []byte(`{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	openaiBody    = []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hey"}]}`)

	anthropicEvents = []*parser.Event{
		{Type: "message_start", Data: []byte(`{"message":{"usage":{"input_tokens":42}}}`)},
		{Type: "message_delta", Data: []byte(`{"usage":{"output_tokens":17},"delta":{"stop_reason":"end_turn"}}`)},
	}

	disp = func() parser.Dispatcher {
		r := parser.NewRegistry()
		r.Register(anthropic.Vendor)
		r.Register(anthropic_proj.Vendor)
		r.Register(openai.Vendor)
		r.Register(passthrough.Vendor)
		return r.Build()
	}()

	costProd = cost.Producer{PerModelRate: map[string]cost.ModelRate{
		"claude-3-5-sonnet": {PromptCentsPer1K: 0.3, CompletionCentsPer1K: 1.5},
		"gpt-4o":            {PromptCentsPer1K: 0.25, CompletionCentsPer1K: 1.0},
	}}
)

// Full per-request lifecycle: Parse + accessors + 2 decoder events +
// cost producer + estimator + K/V reads.
func BenchmarkEndToEnd_Anthropic(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		in, _ := v.Parser.Parse(ctx, anthropicBody)
		_ = in.Model()
		_ = in.Stream()
		_ = in.Prompt()
		for _, ev := range anthropicEvents {
			_ = v.Decoder.Handle(ctx, ev, in)
		}
		costProd.Produce(in)
		estimator.Estimate(in)
		_, _ = kv.Get(in, kv.UsageKey)
		_, _ = kv.Get(in, cost.CostKey)
	}
}

// JSON unmarshal + InferenceInput construction. Dominates the hot path.
func BenchmarkParse_Anthropic(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = v.Parser.Parse(ctx, anthropicBody)
	}
}

func BenchmarkParse_OpenAI(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("openai")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = v.Parser.Parse(ctx, openaiBody)
	}
}

func BenchmarkParse_Passthrough(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("passthrough")
	body := []byte(`opaque embedding request body`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = v.Parser.Parse(ctx, body)
	}
}

// Stateless accessors post-Parse. Proves there is no per-call cost beyond
// pointer-deref + cheap derivation. Prompt allocates because it builds a
// bytes.Buffer; Model/Stream are pure field reads.
func BenchmarkAccessor_Model(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = in.Model()
	}
}

func BenchmarkAccessor_Stream(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = in.Stream()
	}
}

func BenchmarkAccessor_Prompt(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = in.Prompt()
	}
}

// Name → Vendor on the hot path. Map lookup; should be ~tens of ns.
func BenchmarkDispatcherLookup(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = disp.Vendor("anthropic")
	}
}

// Capability assertion: `in.(parser.PromptStructured)`. The whole
// capability-interface argument lives or dies on this being cheap.
func BenchmarkCapabilityAssertion_Implements(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = in.(parser.PromptStructured)
	}
}

func BenchmarkCapabilityAssertion_DoesNot(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("passthrough")
	in, _ := v.Parser.Parse(ctx, []byte("opaque"))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = in.(parser.PromptStructured)
	}
}

// ---- projection variant (anthropic_proj) head-to-head ----

func BenchmarkEndToEnd_AnthropicProj(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic_proj")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		in, _ := v.Parser.Parse(ctx, anthropicBody)
		_ = in.Model()
		_ = in.Stream()
		_ = in.Prompt()
		for _, ev := range anthropicEvents {
			_ = v.Decoder.Handle(ctx, ev, in)
		}
		costProd.Produce(in)
		estimator.Estimate(in)
		_, _ = kv.Get(in, kv.UsageKey)
		_, _ = kv.Get(in, cost.CostKey)
	}
}

func BenchmarkParse_AnthropicProj(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic_proj")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = v.Parser.Parse(ctx, anthropicBody)
	}
}

func BenchmarkAccessor_PromptProj(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic_proj")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = in.Prompt()
	}
}

// One decoder event in isolation: eager (json.Unmarshal) vs projection (gjson).
func BenchmarkDecoderEvent_AnthropicEager(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	ev := anthropicEvents[1] // message_delta — the heaviest typed payload
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = v.Decoder.Handle(ctx, ev, in)
	}
}

func BenchmarkDecoderEvent_AnthropicProj(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic_proj")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	ev := anthropicEvents[1]
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = v.Decoder.Handle(ctx, ev, in)
	}
}

// K/V roundtrip through generic wrappers. Type-safe API vs raw map ops.
func BenchmarkKV_Roundtrip(b *testing.B) {
	ctx := context.Background()
	v, _ := disp.Vendor("anthropic")
	in, _ := v.Parser.Parse(ctx, anthropicBody)
	u := &kv.Usage{Prompt: 42, Completion: 17}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		kv.Update(in, kv.UsageKey, u)
		_, _ = kv.Get(in, kv.UsageKey)
	}
}
