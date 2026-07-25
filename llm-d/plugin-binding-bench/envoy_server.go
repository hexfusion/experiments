package main

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
)

// ConsumerRole is how a body consumer behaves when Envoy hands it the request.
type ConsumerRole int

const (
	// RoleStandalone is a status-quo consumer: it gets the body and parses it
	// itself, because nothing upstream of it shared anything.
	RoleStandalone ConsumerRole = iota
	// RoleShim parses once and fans out to hosted plugins.
	RoleShim
)

// EnvoyExtProc is an ext_proc server that speaks Envoy's real message sequence.
// echoBody reflects the processing mode: FULL_DUPLEX_STREAMED requires the body
// to be returned, BUFFERED does not.
type EnvoyExtProc struct {
	extProcPb.UnimplementedExternalProcessorServer
	plugin    Plugin
	role      ConsumerRole
	echoBody  bool
	hosted    []Binding
	requests  atomic.Int64
	echoBytes atomic.Int64
}

func (s *EnvoyExtProc) Process(stream extProcPb.ExternalProcessor_ProcessServer) error {
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
				if !s.echoBody {
					continue
				}
				continue
			}
			dec, derr := s.decide(stream.Context(), buf)
			if derr != nil {
				return derr
			}
			s.requests.Add(1)

			common := &extProcPb.CommonResponse{
				HeaderMutation: &extProcPb.HeaderMutation{
					SetHeaders: []*corev3.HeaderValueOption{{
						Header: &corev3.HeaderValue{Key: decisionHeader, RawValue: []byte(dec.Endpoint)},
					}},
				},
			}
			if s.echoBody {
				s.echoBytes.Add(int64(len(buf)))
				if err := sendChunkedBody(stream, buf, common); err != nil {
					return err
				}
			} else if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestBody{
					RequestBody: &extProcPb.BodyResponse{Response: common},
				},
			}); err != nil {
				return err
			}
			buf = buf[:0]

		case *extProcPb.ProcessingRequest_RequestTrailers:
			if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestTrailers{
					RequestTrailers: &extProcPb.TrailersResponse{},
				},
			}); err != nil {
				return err
			}
		}
	}
}

func (s *EnvoyExtProc) decide(ctx context.Context, body []byte) (Decision, error) {
	if s.role == RoleStandalone {
		sc, ok := s.plugin.(*scorer)
		if !ok {
			return Decision{}, errors.New("standalone consumer needs a parsing plugin")
		}
		return sc.ParseBodyThenDecide(body)
	}
	return FanOut(ctx, body, s.hosted)
}

// FanOut is the shim path: parse once, then invoke every hosted plugin with the
// resulting metadata.
func FanOut(ctx context.Context, body []byte, hosted []Binding) (Decision, error) {
	meta, err := ParseOnce(body)
	if err != nil {
		return Decision{}, err
	}
	var last Decision
	for _, b := range hosted {
		d, err := b.Invoke(ctx, meta, body)
		if err != nil {
			return Decision{}, err
		}
		last = d
	}
	return last, nil
}

// StartEnvoyConsumer runs a consumer on a fixed port so the static Envoy config
// can find it.
func StartEnvoyConsumer(port int, p Plugin, role ConsumerRole, echoBody bool, hosted []Binding) (*EnvoyExtProc, func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return nil, nil, err
	}
	srv := &EnvoyExtProc{plugin: p, role: role, echoBody: echoBody, hosted: hosted}
	g := grpc.NewServer(grpc.MaxRecvMsgSize(64<<20), grpc.MaxSendMsgSize(64<<20))
	extProcPb.RegisterExternalProcessorServer(g, srv)
	go func() { _ = g.Serve(ln) }()
	return srv, g.Stop, nil
}
