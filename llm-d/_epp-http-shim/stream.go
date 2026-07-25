// Package httpshim lets EPP accept a routing call over plain HTTP without
// changing how it decides anything.
//
// The ext_proc entry point takes a gRPC server stream. Nothing about that
// requires a network: the interface is eight methods, six of which carry no
// meaning outside gRPC. Satisfying it with channels turns the protobuf types
// into an internal calling convention and lets an HTTP handler drive the
// existing StreamingServer.Process unchanged.
package httpshim

import (
	"context"
	"errors"
	"io"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/metadata"
)

var errNotGRPC = errors.New("httpshim: stream is in-process, not gRPC")

// stream is an ExternalProcessor_ProcessServer backed by channels.
type stream struct {
	ctx context.Context
	in  chan *extProcPb.ProcessingRequest
	out chan *extProcPb.ProcessingResponse
}

func newStream(ctx context.Context) *stream {
	return &stream{
		ctx: ctx,
		in:  make(chan *extProcPb.ProcessingRequest, 4),
		out: make(chan *extProcPb.ProcessingResponse, 8),
	}
}

func (s *stream) Recv() (*extProcPb.ProcessingRequest, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case req, ok := <-s.in:
		if !ok {
			return nil, io.EOF
		}
		return req, nil
	}
}

func (s *stream) Send(resp *extProcPb.ProcessingResponse) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.out <- resp:
		return nil
	}
}

func (s *stream) Context() context.Context { return s.ctx }

// The rest of grpc.ServerStream. Headers and trailers are wire concepts with no
// in-process meaning; SendMsg and RecvMsg would bypass the typed pair above.
func (s *stream) SetHeader(metadata.MD) error  { return nil }
func (s *stream) SendHeader(metadata.MD) error { return nil }
func (s *stream) SetTrailer(metadata.MD)       {}
func (s *stream) SendMsg(any) error            { return errNotGRPC }
func (s *stream) RecvMsg(any) error            { return errNotGRPC }

// Compile-time proof that the shim needs no change to the ext_proc contract.
var _ extProcPb.ExternalProcessor_ProcessServer = (*stream)(nil)
