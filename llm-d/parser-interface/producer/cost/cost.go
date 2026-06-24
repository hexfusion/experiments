package cost

import (
	"prototype/kv"
	"prototype/parser"
)

var CostKey = kv.NewKey[float64]("cost_cents")

type ModelRate struct {
	PromptCentsPer1K     float64
	CompletionCentsPer1K float64
}

// Producer reads UsageKey + Model(), writes CostKey.
type Producer struct {
	PerModelRate map[string]ModelRate
}

// Produce runs after the Decoder so UsageKey is populated.
func (p Producer) Produce(in parser.InferenceInput) {
	u, ok := kv.Get(in, kv.UsageKey)
	if !ok {
		return
	}
	rate, ok := p.PerModelRate[in.Model()]
	if !ok {
		return
	}
	cents := float64(u.Prompt)/1000.0*rate.PromptCentsPer1K +
		float64(u.Completion)/1000.0*rate.CompletionCentsPer1K
	kv.Update(in, CostKey, cents)
}
