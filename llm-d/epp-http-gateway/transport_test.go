package main

import (
	"context"
	"testing"
)

// TestTransportsAreInterchangeable is the claim this design rests on: ext_proc
// is a data contract, so an exchange is identical whichever transport carries
// it. Both implementations run against the same unmodified server and must
// produce the same decision.
//
// The grpc-go transport uses the gRPC library. The h2c transport uses none: it
// writes gRPC's five-byte framing onto raw HTTP/2 DATA frames itself.
func TestTransportsAreInterchangeable(t *testing.T) {
	rewritten := `{"messages":[{"content":"hi","role":"user"}],"model":"m-instruct"}`
	f := &fakeEPP{destination: "10.0.0.42:8000", rewriteBody: []byte(rewritten)}
	addr := startFakeEPP(t, f)

	transports := map[string]func() (Transport, error){
		"grpc-go":  func() (Transport, error) { return NewGRPCTransport(addr) },
		"raw-h2c":  func() (Transport, error) { return NewH2CTransport(addr) },
	}

	results := map[string]*RouteResult{}
	for name, mk := range transports {
		tr, err := mk()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Cleanup(func() { _ = tr.Close() })

		c := NewEPPClientWithTransport(tr)
		res, err := c.Route(context.Background(), map[string]string{
			":path":   "/v1/chat/completions",
			":method": "POST",
		}, []byte(chatBody))
		if err != nil {
			t.Fatalf("%s route: %v", name, err)
		}
		if res.Destination != "10.0.0.42:8000" {
			t.Fatalf("%s destination %q", name, res.Destination)
		}
		if !res.BodyModified || string(res.Body) != rewritten {
			t.Fatalf("%s did not carry the rewritten body back", name)
		}
		results[name] = res
	}

	a, b := results["grpc-go"], results["raw-h2c"]
	if a.Destination != b.Destination {
		t.Fatalf("transports disagreed on destination: %q vs %q", a.Destination, b.Destination)
	}
	if string(a.Body) != string(b.Body) {
		t.Fatal("transports disagreed on the body to forward")
	}
	if len(a.SetHeaders) != len(b.SetHeaders) {
		t.Fatalf("transports disagreed on header count: %d vs %d", len(a.SetHeaders), len(b.SetHeaders))
	}
}

// TestH2CTransportPropagatesImmediate checks the refusal path travels the raw
// transport too, since admission control arriving as a decision would be worse
// than it not arriving at all.
func TestH2CTransportPropagatesImmediate(t *testing.T) {
	f := &fakeEPP{shed: &Immediate{Status: 429, Body: "saturated"}}
	addr := startFakeEPP(t, f)
	tr, err := NewH2CTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })

	res, err := NewEPPClientWithTransport(tr).Route(context.Background(),
		map[string]string{":path": "/v1/chat/completions"}, []byte(chatBody))
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if res.Immediate == nil {
		t.Fatal("no immediate response over the raw transport")
	}
	if res.Immediate.Status != 429 {
		t.Fatalf("status %d, want 429", res.Immediate.Status)
	}
}
