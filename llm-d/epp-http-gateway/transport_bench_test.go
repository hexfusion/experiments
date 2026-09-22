package main

import (
	"context"
	"strings"
	"testing"
)

// Benchmarks compare the two transports on identical exchanges against the same
// server. grpc-go ships its own HTTP/2 stack with buffered writers and a shared
// buffer pool; the raw implementation uses the standard library's h2 transport
// with an io.Pipe, which is unbuffered and synchronises every write against a
// read. Whether that costs anything at these message sizes is the question.
func benchTransport(b *testing.B, mk func(string) (Transport, error), bodyKB int) {
	f := &fakeEPP{destination: "10.0.0.1:8000"}
	addr := startFakeEPPB(b, f)
	tr, err := mk(addr)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tr.Close() })
	c := NewEPPClientWithTransport(tr)

	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` +
		strings.Repeat("x", bodyKB<<10) + `"}]}`)
	hdrs := map[string]string{":path": "/v1/chat/completions", ":method": "POST"}

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Route(context.Background(), hdrs, body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGRPCTransport_4KB(b *testing.B)  { benchTransport(b, NewGRPCTransport, 4) }
func BenchmarkH2CTransport_4KB(b *testing.B)   { benchTransport(b, NewH2CTransport, 4) }
func BenchmarkGRPCTransport_128KB(b *testing.B) { benchTransport(b, NewGRPCTransport, 128) }
func BenchmarkH2CTransport_128KB(b *testing.B)  { benchTransport(b, NewH2CTransport, 128) }

// Concurrency is where a shared connection and a per-stream pipe diverge most.
func benchTransportParallel(b *testing.B, mk func(string) (Transport, error)) {
	f := &fakeEPP{destination: "10.0.0.1:8000"}
	addr := startFakeEPPB(b, f)
	tr, err := mk(addr)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tr.Close() })
	c := NewEPPClientWithTransport(tr)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` +
		strings.Repeat("x", 16<<10) + `"}]}`)
	hdrs := map[string]string{":path": "/v1/chat/completions", ":method": "POST"}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := c.Route(context.Background(), hdrs, body); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkGRPCTransport_Parallel(b *testing.B) { benchTransportParallel(b, NewGRPCTransport) }
func BenchmarkH2CTransport_Parallel(b *testing.B)  { benchTransportParallel(b, NewH2CTransport) }
