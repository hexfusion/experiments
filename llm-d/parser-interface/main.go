package main

import (
	"bytes"
	"context"
	"fmt"

	"prototype/kv"
	"prototype/parser"
	"prototype/producer/cost"
	"prototype/producer/estimator"
	"prototype/vendors/anthropic"
	"prototype/vendors/openai"
	"prototype/vendors/passthrough"
)

func main() {
	ctx := context.Background()

	reg := parser.NewRegistry()
	reg.Register(anthropic.Vendor)
	reg.Register(openai.Vendor)
	reg.Register(passthrough.Vendor)
	disp := reg.Build()

	costProducer := cost.Producer{
		PerModelRate: map[string]cost.ModelRate{
			"claude-3-5-sonnet": {PromptCentsPer1K: 0.3, CompletionCentsPer1K: 1.5},
			"gpt-4o":            {PromptCentsPer1K: 0.25, CompletionCentsPer1K: 1.0},
		},
	}

	fmt.Println("=== anthropic ===")
	runRequest(ctx, disp, costProducer, "anthropic",
		[]byte(`{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hello"}]}`),
		[]*parser.Event{
			{Type: "message_start", Data: []byte(`{"message":{"usage":{"input_tokens":42}}}`)},
			{Type: "content_block_delta", Data: []byte(`{}`)},
			{Type: "message_delta", Data: []byte(`{"usage":{"output_tokens":17},"delta":{"stop_reason":"end_turn"}}`)},
			{Type: "message_stop", Data: []byte(`{}`)},
		},
	)

	fmt.Println("\n=== openai ===")
	runRequest(ctx, disp, costProducer, "openai",
		[]byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hey"}]}`),
		[]*parser.Event{
			{Type: "chunk", Data: []byte(`{"choices":[{"finish_reason":""}]}`)},
			{Type: "chunk", Data: []byte(`{"choices":[{"finish_reason":""}]}`)},
			{Type: "chunk", Data: []byte(`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":23}}`)},
		},
	)

	fmt.Println("\n=== passthrough ===")
	runRequest(ctx, disp, costProducer, "passthrough",
		[]byte(`opaque embedding request body`),
		nil,
	)

	fmt.Println("\n=== unknown vendor ===")
	runRequest(ctx, disp, costProducer, "vertex", nil, nil)
}

func runRequest(
	ctx context.Context,
	disp parser.Dispatcher,
	costProducer cost.Producer,
	name string,
	body []byte,
	events []*parser.Event,
) {
	v, ok := disp.Vendor(name)
	if !ok {
		fmt.Printf("unknown vendor: %q\n", name)
		return
	}
	in, err := v.Parser.Parse(ctx, body)
	if err != nil {
		fmt.Println("parse error:", err)
		return
	}
	if m := in.Model(); m != "" {
		fmt.Println("model: ", m)
	}
	fmt.Println("stream:", in.Stream())
	if p := in.Prompt(); len(p) > 0 {
		fmt.Printf("prompt: %s", p)
		if !bytes.HasSuffix(p, []byte("\n")) {
			fmt.Println()
		}
	}

	for _, ev := range events {
		if err := v.Decoder.Handle(ctx, ev, in); err != nil {
			fmt.Println("decode error:", err)
			return
		}
	}

	costProducer.Produce(in)
	estimator.Estimate(in)

	if u, ok := kv.Get(in, kv.UsageKey); ok {
		fmt.Printf("usage:  prompt=%d completion=%d\n", u.Prompt, u.Completion)
	}
	if fr, ok := kv.Get(in, kv.FinishReasonKey); ok {
		fmt.Printf("finish: %s\n", fr)
	}
	if c, ok := kv.Get(in, cost.CostKey); ok {
		fmt.Printf("cost:   %.4f cents\n", c)
	}
	if h, ok := kv.Get(in, estimator.PrefixHashKey); ok {
		_, structured := in.(parser.PromptStructured)
		fmt.Printf("hash:   %016x (structured=%t)\n", h, structured)
	}

	emitMetrics(in)
}

// emitMetrics takes kv.Reader so the type system gates it from writing.
func emitMetrics(r kv.Reader) {
	if u, ok := kv.Get(r, kv.UsageKey); ok {
		fmt.Printf("metric: usage_tokens prompt=%d completion=%d\n", u.Prompt, u.Completion)
	}
	if c, ok := kv.Get(r, cost.CostKey); ok {
		fmt.Printf("metric: cost_cents value=%.4f\n", c)
	}
}
