package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
)

// fakeEPP behaves the way llm-d's EPP does on the wire: it answers headers,
// buffers the body, and returns a destination header plus the body chunked back
// as a StreamedResponse, which is what FULL_DUPLEX_STREAMED requires.
type fakeEPP struct {
	extProcPb.UnimplementedExternalProcessorServer
	destination string
	// rewriteBody stands in for Director.repackage re-marshalling the payload.
	rewriteBody []byte
	shed        *Immediate
	sawBody     []byte
	sawPath     string
}

func (f *fakeEPP) Process(stream extProcPb.ExternalProcessor_ProcessServer) error {
	var buf []byte
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			for _, h := range v.RequestHeaders.GetHeaders().GetHeaders() {
				if h.Key == ":path" {
					f.sawPath = string(h.RawValue)
				}
			}
			if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extProcPb.HeadersResponse{Response: &extProcPb.CommonResponse{}},
				},
			}); err != nil {
				return err
			}
		case *extProcPb.ProcessingRequest_RequestBody:
			buf = append(buf, v.RequestBody.GetBody()...)
			if !v.RequestBody.GetEndOfStream() {
				continue
			}
			f.sawBody = append([]byte(nil), buf...)

			if f.shed != nil {
				return stream.Send(&extProcPb.ProcessingResponse{
					Response: &extProcPb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: &extProcPb.ImmediateResponse{
							Status: &typev3.HttpStatus{Code: typev3.StatusCode(f.shed.Status)},
							Body:   []byte(f.shed.Body),
						},
					},
				})
			}

			out := buf
			if f.rewriteBody != nil {
				out = f.rewriteBody
			}
			common := &extProcPb.CommonResponse{
				HeaderMutation: &extProcPb.HeaderMutation{
					SetHeaders: []*corev3.HeaderValueOption{{
						Header: &corev3.HeaderValue{
							Key: destinationHeader, RawValue: []byte(f.destination),
						},
					}},
				},
				BodyMutation: &extProcPb.BodyMutation{
					Mutation: &extProcPb.BodyMutation_StreamedResponse{
						StreamedResponse: &extProcPb.StreamedBodyResponse{
							Body: out, EndOfStream: true,
						},
					},
				},
			}
			if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestBody{
					RequestBody: &extProcPb.BodyResponse{Response: common},
				},
			}); err != nil {
				return err
			}
			buf = nil
		}
	}
}

// routeDecision is the request-phase response llm-d's EPP sends: a destination
// header plus the body returned chunked, which FULL_DUPLEX_STREAMED requires.
func routeDecision(destination string, body []byte) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestBody{
			RequestBody: &extProcPb.BodyResponse{Response: &extProcPb.CommonResponse{
				HeaderMutation: &extProcPb.HeaderMutation{
					SetHeaders: []*corev3.HeaderValueOption{{
						Header: &corev3.HeaderValue{
							Key: destinationHeader, RawValue: []byte(destination),
						},
					}},
				},
				BodyMutation: &extProcPb.BodyMutation{
					Mutation: &extProcPb.BodyMutation_StreamedResponse{
						StreamedResponse: &extProcPb.StreamedBodyResponse{
							Body: body, EndOfStream: true,
						},
					},
				},
			}},
		},
	}
}

func startFakeEPP(t *testing.T, f extProcPb.ExternalProcessorServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, f)
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(s.Stop)
	return ln.Addr().String()
}

// startGateway runs the real gateway over h2c, which is how a data plane would
// reach it.
func startGateway(t *testing.T, eppAddr string) (string, *Gateway) {
	t.Helper()
	epp, err := NewEPPClient(eppAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = epp.Close() })
	g := NewGateway(epp)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ServeH2C(ln, g.Handler()) }()
	t.Cleanup(func() { _ = ln.Close() })
	return "http://" + ln.Addr().String(), g
}

// h2cClient is the data plane's side: a plain HTTP client. No gRPC, no ext_proc.
func h2cClient() *http.Client {
	return &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

const chatBody = `{"model":"llama-3.1-8b","stream":true,"messages":[{"role":"user","content":"hi"}]}`

func route(t *testing.T, base string, body string) (*http.Response, Decision, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/route", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-original-path", "/v1/chat/completions")
	resp, err := h2cClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var dec Decision
	if raw := resp.Header.Get(decisionHeader); raw != "" {
		if err := json.Unmarshal([]byte(raw), &dec); err != nil {
			t.Fatalf("decode decision: %v", err)
		}
	}
	return resp, dec, out
}

// TestRoutingDecisionOverPlainHTTP is the point of this edge: a caller that
// speaks only HTTP gets a routing decision from an unmodified EPP.
func TestRoutingDecisionOverPlainHTTP(t *testing.T) {
	f := &fakeEPP{destination: "10.0.0.7:8000"}
	base, g := startGateway(t, startFakeEPP(t, f))

	resp, dec, out := route(t, base, chatBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if dec.Destination != "10.0.0.7:8000" {
		t.Fatalf("destination %q", dec.Destination)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("expected HTTP/2, got %d", resp.ProtoMajor)
	}
	if string(out) != chatBody {
		t.Fatalf("body changed unexpectedly")
	}
	if f.sawPath != "/v1/chat/completions" {
		t.Fatalf("EPP saw path %q", f.sawPath)
	}
	if string(f.sawBody) != chatBody {
		t.Fatalf("EPP saw a different body than was sent")
	}
	if routed, _, _, failures := g.Stats(); routed != 1 || failures != 0 {
		t.Fatalf("stats routed=%d failures=%d", routed, failures)
	}
}

// TestRewrittenBodyIsReturned covers the byte-fidelity case. llm-d re-marshals
// every OpenAI-parsed request, so the caller must forward what EPP returned
// rather than what it sent, or model rewrites are lost.
func TestRewrittenBodyIsReturned(t *testing.T) {
	rewritten := `{"messages":[{"content":"hi","role":"user"}],"model":"llama-3.1-8b-instruct","stream":true}`
	f := &fakeEPP{destination: "10.0.0.2:8000", rewriteBody: []byte(rewritten)}
	base, g := startGateway(t, startFakeEPP(t, f))

	_, dec, out := route(t, base, chatBody)
	if !dec.BodyModified {
		t.Fatal("decision did not report the body as modified")
	}
	if string(out) != rewritten {
		t.Fatalf("returned body is not EPP's rewrite")
	}
	if _, _, rewrote, _ := g.Stats(); rewrote != 1 {
		t.Fatal("rewrite not counted")
	}
}

// TestImmediateResponseIsPropagated covers admission control: EPP declining
// must reach the caller as a refusal, not as a routing decision.
func TestImmediateResponseIsPropagated(t *testing.T) {
	f := &fakeEPP{shed: &Immediate{Status: 429, Body: "saturated"}}
	base, g := startGateway(t, startFakeEPP(t, f))

	resp, dec, out := route(t, base, chatBody)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", resp.StatusCode)
	}
	if string(out) != "saturated" {
		t.Fatalf("body %q", out)
	}
	if dec.Destination != "" {
		t.Fatal("a shed request must not carry a destination")
	}
	if _, shed, _, _ := g.Stats(); shed != 1 {
		t.Fatal("shed not counted")
	}
}

// TestLargeBodyChunking exercises the 62KB chunking in both directions, which
// is where the protocol details would otherwise leak to the caller.
func TestLargeBodyChunking(t *testing.T) {
	big := `{"model":"m","messages":[{"role":"user","content":"` +
		string(bytes.Repeat([]byte("x"), 200<<10)) + `"}]}`
	f := &fakeEPP{destination: "10.0.0.3:8000"}
	base, _ := startGateway(t, startFakeEPP(t, f))

	_, dec, out := route(t, base, big)
	if dec.Destination != "10.0.0.3:8000" {
		t.Fatalf("destination %q", dec.Destination)
	}
	if len(f.sawBody) != len(big) {
		t.Fatalf("EPP saw %d bytes, sent %d", len(f.sawBody), len(big))
	}
	if len(out) != len(big) {
		t.Fatalf("got %d bytes back, sent %d", len(out), len(big))
	}
}
