package kv

type Reader interface {
	Get(name string) (any, bool)
}

type Writer interface {
	Update(name string, value any)
	Remove(name string)
}

type Store interface {
	Reader
	Writer
}

// Key[T] is a typed handle for a value of type T in a Store. Declare keys
// once with NewKey and pass them to Get, Update, and Remove for type-safe
// access.
type Key[T any] struct{ name string }

// NewKey declares a typed key. The name identifies the value in the store;
// T constrains the value's type at every call site.
func NewKey[T any](name string) Key[T] { return Key[T]{name: name} }

func (k Key[T]) Name() string { return k.name }

// Get returns (zero, false) if the key is missing or stored under a value
// whose type differs from T.
func Get[T any](r Reader, k Key[T]) (T, bool) {
	v, ok := r.Get(k.name)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	if !ok {
		var zero T
		return zero, false
	}
	return t, true
}

func Update[T any](w Writer, k Key[T], v T) { w.Update(k.name, v) }
func Remove[T any](w Writer, k Key[T])      { w.Remove(k.name) }

// BaseAttrs is an embeddable Store implementation.
type BaseAttrs struct {
	m map[string]any
}

func NewBaseAttrs() BaseAttrs { return BaseAttrs{m: map[string]any{}} }

func (b *BaseAttrs) Get(name string) (any, bool) { v, ok := b.m[name]; return v, ok }
func (b *BaseAttrs) Update(name string, v any)   { b.m[name] = v }
func (b *BaseAttrs) Remove(name string)          { delete(b.m, name) }

type Usage struct {
	Prompt     int
	Completion int
}

var (
	UsageKey        = NewKey[*Usage]("usage")
	FinishReasonKey = NewKey[string]("finish_reason")
)
