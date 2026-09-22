package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// grpcTransport is the conventional implementation: grpc-go against an
// unmodified endpoint picker.
type grpcTransport struct {
	conn   *grpc.ClientConn
	client extProcPb.ExternalProcessorClient
}

// NewGRPCTransport dials with client-side round-robin.
//
// This matters more than it looks. A single gRPC connection multiplexes every
// stream over one TCP connection, so a plain dial at a Service VIP sends all
// traffic to one picker pod however many replicas are running, and an L4
// balancer cannot spread what is only one connection. Resolving a headless
// service and round-robining across the resulting subconnections is what
// actually distributes load.
func NewGRPCTransport(addr string) (Transport, error) {
	target := addr
	if !strings.Contains(target, "://") {
		target = "dns:///" + target
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(64<<20),
			grpc.MaxCallSendMsgSize(64<<20),
		))
	if err != nil {
		return nil, err
	}
	return &grpcTransport{conn: conn, client: extProcPb.NewExternalProcessorClient(conn)}, nil
}

func (t *grpcTransport) Open(ctx context.Context) (ProcessStream, error) {
	s, err := t.client.Process(ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (t *grpcTransport) Close() error { return t.conn.Close() }

// Ping asks the connection to leave idle and waits briefly for a usable state,
// rather than opening an exchange, so probing costs the picker nothing.
func (t *grpcTransport) Ping(ctx context.Context) error {
	t.conn.Connect()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		switch s := t.conn.GetState(); s {
		case connectivity.Ready, connectivity.Idle:
			return nil
		case connectivity.Shutdown:
			return errors.New("connection shut down")
		default:
			if !t.conn.WaitForStateChange(ctx, s) {
				return fmt.Errorf("picker not reachable, state %s", s)
			}
		}
	}
}
