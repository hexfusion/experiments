package main

import "context"

// Binding is how a plugin attaches to the shim. The plugin logic is identical
// across bindings; only the path the data takes differs.
type Binding interface {
	Name() string
	// Invoke runs one consumer. body is passed for the status-quo binding that
	// re-parses it; bindings that consume metadata must ignore it.
	Invoke(ctx context.Context, m *Metadata, body []byte) (Decision, error)
	// WireBytes is the total payload put on a transport so far, zero for
	// in-process bindings.
	WireBytes() int64
	Close() error
}
