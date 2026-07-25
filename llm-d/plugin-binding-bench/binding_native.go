package main

import "context"

// nativeBinding is the reference floor: the plugin is compiled in and called
// directly. No serialization, no boundary. Nothing can beat this.
type nativeBinding struct {
	plugin Plugin
}

func NewNativeBinding(p Plugin) Binding { return &nativeBinding{plugin: p} }

func (b *nativeBinding) Name() string     { return "native" }
func (b *nativeBinding) WireBytes() int64 { return 0 }
func (b *nativeBinding) Close() error     { return nil }

func (b *nativeBinding) Invoke(_ context.Context, m *Metadata, _ []byte) (Decision, error) {
	return b.plugin.Decide(m), nil
}
