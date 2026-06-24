package parser

import (
	"context"
	"encoding/json"
	"maps"

	"prototype/kv"
)

// InferenceInput is the bounded typed-accessor surface every parsed vendor
// exposes, and the per-request K/V store. New concerns go through the
// embedded kv.Store, not new interface methods.
type InferenceInput interface {
	Model() string
	Stream() bool
	Prompt() []byte
	kv.Store
}

// Parser.Parse is the validation boundary: on success the returned
// InferenceInput's accessors are guaranteed.
type Parser interface {
	Parse(ctx context.Context, body []byte) (InferenceInput, error)
}

type Event struct {
	Type string
	Data []byte
}

type EventHandler func(ctx context.Context, data []byte, in InferenceInput) error

type TypedHandler[T any] func(ctx context.Context, v T, in InferenceInput) error

// DecodeJSON wraps a TypedHandler[T] with json.Unmarshal; T is inferred
// from h's signature so the call site has no generic syntax.
func DecodeJSON[T any](h TypedHandler[T]) EventHandler {
	return func(ctx context.Context, data []byte, in InferenceInput) error {
		var v T
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		return h(ctx, v, in)
	}
}

type Decoder struct {
	handlers map[string]EventHandler
}

// NewDecoder copies handlers so post-construct mutations do not affect the Decoder.
func NewDecoder(handlers map[string]EventHandler) Decoder {
	m := make(map[string]EventHandler, len(handlers))
	maps.Copy(m, handlers)
	return Decoder{handlers: m}
}

// Handle silently skips events with no registered handler.
func (d Decoder) Handle(ctx context.Context, ev *Event, in InferenceInput) error {
	h, ok := d.handlers[ev.Type]
	if !ok {
		return nil
	}
	return h(ctx, ev.Data, in)
}

// Vendor is one API surface. Zero Decoder is valid for vendors with no
// response signals.
type Vendor struct {
	Name    string
	Parser  Parser
	Decoder Decoder
}

type Registry struct {
	vendors map[string]Vendor
}

func NewRegistry() *Registry { return &Registry{vendors: map[string]Vendor{}} }

func (r *Registry) Register(v Vendor) { r.vendors[v.Name] = v }

// Build freezes the Registry; subsequent Register calls do not affect the
// returned Dispatcher.
func (r *Registry) Build() Dispatcher {
	return Dispatcher{vendors: maps.Clone(r.vendors)}
}

type Dispatcher struct {
	vendors map[string]Vendor
}

func (d Dispatcher) Vendor(name string) (Vendor, bool) {
	v, ok := d.vendors[name]
	return v, ok
}

// PromptStructured is a capability interface. Vendors with native block
// structure (Anthropic Messages, OpenAI Chat content arrays) implement it;
// consumers needing block iteration query for it via type assertion and
// fall back to InferenceInput.Prompt() when absent. This keeps the primary
// InferenceInput bounded while giving block-aware consumers a vendor-neutral
// API. Same pattern as io.ReaderAt / http.Hijacker.
type PromptStructured interface {
	PromptBlocks() []Block
}

type BlockKind int

const (
	BlockText BlockKind = iota
	BlockImageRef
)

// Block is the vendor-neutral structured-prompt unit. New kinds extend this
// type or move to a kind-specific sub-interface; vendors translate from
// their internal representation at Parse time.
type Block struct {
	Kind BlockKind
	Text string // BlockText
	Ref  []byte // BlockImageRef (opaque vendor-shaped reference)
}
