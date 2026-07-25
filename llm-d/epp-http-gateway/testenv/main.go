// Command testenv runs a stand-in ext_proc processor and an upstream, so the
// gateway and a data plane can be exercised end to end without a cluster.
//
// The processor behaves the way llm-d's EPP does on the wire: it answers
// headers, buffers the body, picks an endpoint, re-serializes the request the
// way Director.repackage does, and returns the body chunked as a
// StreamedResponse because FULL_DUPLEX_STREAMED requires it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/grpc"
)

const destinationHeader = "x-gateway-destination-endpoint"

type processor struct {
	extProcPb.UnimplementedExternalProcessorServer
	endpoints []string
	rejectOver int
	calls     atomic.Int64
}

func (p *processor) Process(stream extProcPb.ExternalProcessor_ProcessServer) error {
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
			n := p.calls.Add(1)

			// Admission control, as EPP does when saturated.
			if p.rejectOver > 0 && int(n) > p.rejectOver {
				return stream.Send(&extProcPb.ProcessingResponse{
					Response: &extProcPb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: &extProcPb.ImmediateResponse{
							Status: &typev3.HttpStatus{Code: typev3.StatusCode(429)},
							Body:   []byte("saturated"),
						},
					},
				})
			}

			dest := p.endpoints[int(n-1)%len(p.endpoints)]
			out := repackage(buf)
			common := &extProcPb.CommonResponse{
				HeaderMutation: &extProcPb.HeaderMutation{
					SetHeaders: []*corev3.HeaderValueOption{{
						Header: &corev3.HeaderValue{Key: destinationHeader, RawValue: []byte(dest)},
					}},
				},
				BodyMutation: &extProcPb.BodyMutation{
					Mutation: &extProcPb.BodyMutation_StreamedResponse{
						StreamedResponse: &extProcPb.StreamedBodyResponse{Body: out, EndOfStream: true},
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
			fmt.Fprintf(os.Stderr, "processor: request %d -> %s (%d bytes in, %d out)\n",
				n, dest, len(buf), len(out))
			buf = nil
		}
	}
}

// repackage reproduces the round trip through map[string]any that llm-d does,
// which sorts keys and floats every number. It is what makes the returned body
// differ from the received one.
func repackage(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

func main() {
	eppAddr := flag.String("epp", "127.0.0.1:9002", "ext_proc listen address")
	upstreamAddr := flag.String("upstream", "127.0.0.1:9300", "upstream listen address")
	endpoints := flag.String("endpoints", "10.0.0.1:8000,10.0.0.2:8000", "endpoints to hand out")
	rejectOver := flag.Int("reject-over", 0, "reject requests after this many, 0 disables")
	flag.Parse()

	ln, err := net.Listen("tcp", *eppAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen epp:", err)
		os.Exit(1)
	}
	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, &processor{
		endpoints:  strings.Split(*endpoints, ","),
		rejectOver: *rejectOver,
	})
	go func() { _ = s.Serve(ln) }()
	fmt.Printf("processor (ext_proc) on %s\n", *eppAddr)

	// Upstream echoes what it received, so a test can prove which body arrived
	// and which destination header the data plane applied.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		keys := make([]string, 0, len(r.Header))
		for k := range r.Header {
			keys = append(keys, strings.ToLower(k))
		}
		sort.Strings(keys)
		fmt.Fprintf(os.Stderr, "upstream: %s %s (%d bytes)\n", r.Method, r.URL.Path, len(body))
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"received_body":  string(body),
			"header_keys":    keys,
			"destination":    r.Header.Get(destinationHeader),
			"content_length": len(body),
		})
	})
	fmt.Printf("upstream on %s\n", *upstreamAddr)
	if err := http.ListenAndServe(*upstreamAddr, mux); err != nil {
		fmt.Fprintln(os.Stderr, "upstream:", err)
		os.Exit(1)
	}
}
