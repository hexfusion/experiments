package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// startPlugin runs a plugin as what it is: an HTTP handler.
func startPlugin(t *testing.T, fn func(*Metadata) Decision) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/decide", func(w http.ResponseWriter, r *http.Request) {
		var m Metadata
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(fn(&m))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func startBridge(t *testing.T, plugins []PluginSpec) (extProcPb.ExternalProcessorClient, *Bridge) {
	t.Helper()
	b := NewBridge(plugins, OpenAIExtractor{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, b)
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return extProcPb.NewExternalProcessorClient(conn), b
}

// drive replays the message sequence a gateway actually sends.
func drive(t *testing.T, c extProcPb.ExternalProcessorClient, body []byte) []*extProcPb.ProcessingResponse {
	t.Helper()
	stream, err := c.Process(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hdrs := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
		{Key: ":path", RawValue: []byte("/v1/chat/completions")},
		{Key: ":method", RawValue: []byte("POST")},
		{Key: "x-request-id", RawValue: []byte("req-1")},
	}}
	if err := stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{Headers: hdrs},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{Body: body, EndOfStream: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_ = stream.CloseSend()

	var out []*extProcPb.ProcessingResponse
	for {
		r, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		out = append(out, r)
	}
	return out
}

const chatBody = `{"model":"llama-3.1-8b","stream":true,"messages":[{"role":"user","content":"hello there this is a prompt"}]}`

// TestRoutingDecisionFromMetadata is the whole point: a plugin that never sees
// bytes and never speaks gRPC decides where the request goes.
func TestRoutingDecisionFromMetadata(t *testing.T) {
	var gotModel string
	var gotBodyBytes int
	p := startPlugin(t, func(m *Metadata) Decision {
		gotModel = m.Model
		gotBodyBytes = m.BodyBytes
		return Decision{SetHeaders: map[string]string{"x-gateway-destination-endpoint": "10.0.0.7:8000"}}
	})
	c, _ := startBridge(t, []PluginSpec{{Name: "picker", URL: p.URL}})

	resps := drive(t, c, []byte(chatBody))
	if len(resps) < 2 {
		t.Fatalf("expected headers and body responses, got %d", len(resps))
	}
	if gotModel != "llama-3.1-8b" {
		t.Fatalf("plugin saw model %q", gotModel)
	}
	if gotBodyBytes != len(chatBody) {
		t.Fatalf("plugin saw body_bytes %d, want %d", gotBodyBytes, len(chatBody))
	}

	last := resps[len(resps)-1].GetRequestBody().GetResponse()
	var found string
	for _, h := range last.GetHeaderMutation().GetSetHeaders() {
		if h.GetHeader().GetKey() == "x-gateway-destination-endpoint" {
			found = string(h.GetHeader().GetRawValue())
		}
	}
	if found != "10.0.0.7:8000" {
		t.Fatalf("destination header not set, got %q", found)
	}
}

// TestNoBodyReturnedWhenNothingMutates is the mode consequence of the
// declaration: with no plugin declaring mutation, the body never crosses back.
func TestNoBodyReturnedWhenNothingMutates(t *testing.T) {
	p := startPlugin(t, func(m *Metadata) Decision { return Decision{} })
	c, b := startBridge(t, []PluginSpec{{Name: "observer", URL: p.URL}})
	if b.returnBody {
		t.Fatal("bridge should not return the body when nothing declares mutation")
	}
	resps := drive(t, c, []byte(chatBody))
	last := resps[len(resps)-1].GetRequestBody().GetResponse()
	if last.GetBodyMutation() != nil {
		t.Fatal("body mutation returned despite no plugin declaring mutation")
	}
}

func TestBodyReturnedWhenMutationDeclared(t *testing.T) {
	p := startPlugin(t, func(m *Metadata) Decision { return Decision{} })
	c, b := startBridge(t, []PluginSpec{{Name: "rewriter", URL: p.URL, MutatesRequest: true}})
	if !b.returnBody {
		t.Fatal("bridge should return the body when mutation is declared")
	}
	resps := drive(t, c, []byte(chatBody))
	last := resps[len(resps)-1].GetRequestBody().GetResponse()
	if last.GetBodyMutation() == nil {
		t.Fatal("expected a body mutation")
	}
}

// TestImmediateResponse covers the auth and rate-limit shape: a plugin ends the
// request without the backend being contacted.
func TestImmediateResponse(t *testing.T) {
	p := startPlugin(t, func(m *Metadata) Decision {
		return Decision{Immediate: &ImmediateResponse{Status: 429, Body: "over limit"}}
	})
	c, _ := startBridge(t, []PluginSpec{{Name: "limiter", URL: p.URL}})

	resps := drive(t, c, []byte(chatBody))
	last := resps[len(resps)-1]
	ir := last.GetImmediateResponse()
	if ir == nil {
		t.Fatal("expected an immediate response")
	}
	if got := int(ir.GetStatus().GetCode()); got != 429 {
		t.Fatalf("status %d, want 429", got)
	}
}

// TestChainSharesAttributes shows the second plugin reading what the first
// published, without either of them parsing the body.
func TestChainSharesAttributes(t *testing.T) {
	first := startPlugin(t, func(m *Metadata) Decision {
		return Decision{Publish: map[string]any{"tier": "gold"}}
	})
	var sawTier any
	second := startPlugin(t, func(m *Metadata) Decision {
		sawTier = m.Attributes["first.tier"]
		return Decision{}
	})
	c, _ := startBridge(t, []PluginSpec{
		{Name: "first", URL: first.URL},
		{Name: "second", URL: second.URL},
	})
	drive(t, c, []byte(chatBody))
	if sawTier != "gold" {
		t.Fatalf("second plugin saw tier %v, want gold", sawTier)
	}
}
