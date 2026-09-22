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
	return s.HandleAttempts(ctx, body, 1)
}

// HandleAttempts runs one client request that becomes n upstream attempts, which
// is what retry, best-of-N, and inference-time-scaling fan-out do.
//
// This is where single ownership pays most. Owning the bytes means extracting
// once and reusing across attempts; a chain of independent consumers re-extracts
// per attempt, and extraction is a synchronous render call.
func (s *Shim) HandleAttempts(ctx context.Context, body []byte, attempts int) (Decision, error) {
	var meta *Metadata
	if s.parseOnce {
		m, err := globalExtractor.ExtractCtx(ctx, body)
		if err != nil {
			return Decision{}, err
		}
		meta = m
	}

	var last Decision
	for a := 0; a < attempts; a++ {
		for _, b := range s.bindings {
			d, err := b.Invoke(ctx, meta, body)
			if err != nil {
				return Decision{}, err
			}
			last = d
		}
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
