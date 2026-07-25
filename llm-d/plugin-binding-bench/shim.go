package main

import "context"

// Shim is the in-process stage that owns the request data lifecycle. It
// materializes the body once, parses once, and fans the result out to every
// attached consumer through that consumer's binding.
//
// parseOnce false models the status quo, where the shim does no parse and each
// consumer re-parses the body for itself.
type Shim struct {
	bindings  []Binding
	parseOnce bool
}

func NewShim(parseOnce bool, bindings ...Binding) *Shim {
	return &Shim{bindings: bindings, parseOnce: parseOnce}
}

// Handle runs one request through every consumer and returns the last decision.
func (s *Shim) Handle(ctx context.Context, body []byte) (Decision, error) {
	var meta *Metadata
	if s.parseOnce {
		m, err := globalExtractor.Extract(body)
		if err != nil {
			return Decision{}, err
		}
		meta = m
	}

	var last Decision
	for _, b := range s.bindings {
		d, err := b.Invoke(ctx, meta, body)
		if err != nil {
			return Decision{}, err
		}
		last = d
	}
	return last, nil
}

func (s *Shim) WireBytes() int64 {
	var total int64
	for _, b := range s.bindings {
		total += b.WireBytes()
	}
	return total
}

func (s *Shim) Close() {
	for _, b := range s.bindings {
		_ = b.Close()
	}
}
