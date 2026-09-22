package main

import (
	"context"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// ProcessStream is the ext_proc exchange reduced to what a caller actually
// needs. Three methods, which is the whole surface this gateway uses against
// grpc-go today.
//
// That it is this small is the point. ext_proc is a data contract, and the
// contract is "send these messages, receive those messages, then stop". How the
// bytes travel is a separate question, which is what Transport answers.
type ProcessStream interface {
	Send(*extProcPb.ProcessingRequest) error
	Recv() (*extProcPb.ProcessingResponse, error)
	CloseSend() error
}

// Transport opens ext_proc streams. Implementations differ only in wire
// mechanics; every exchange above them is identical.
//
// Three are possible and two are implemented. grpc-go against an unmodified
// endpoint picker. Raw cleartext HTTP/2 carrying gRPC's framing, also against an
// unmodified picker but with no gRPC dependency. And eventually raw HTTP/2
// carrying our own framing, against a picker that accepts the contract
// natively. The first two differ by a five-byte prefix convention and a URL
// path, which is the concrete evidence that the protocol is not the hard part.
type Transport interface {
	Open(ctx context.Context) (ProcessStream, error)
	// Ping reports reachability for readiness without opening an exchange.
	Ping(ctx context.Context) error
	Close() error
}
