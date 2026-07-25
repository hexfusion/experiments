package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// EPPClient speaks ext_proc to an unchanged EPP.
//
// Everything unpleasant about the protocol lives here: the phase sequence, the
// requirement to answer nothing but to receive everything in order, the body
// arriving back in 62KB chunks because llm-d runs FULL_DUPLEX_STREAMED, and the
// fact that a decision can arrive as a header mutation on either the headers or
// the body response.
type EPPClient struct {
	conn   *grpc.ClientConn
	client extProcPb.ExternalProcessorClient
}

// NewEPPClient dials EPP with client-side round-robin.
//
// This matters more than it looks. A single gRPC connection multiplexes every
// stream over one TCP connection, so a plain dial at a Service VIP sends all
// traffic to one EPP pod no matter how many replicas exist, and an L4 balancer
// cannot spread it because there is only one connection to spread. Resolving a
// headless service and round-robining across the resulting subconnections is
// what actually distributes load.
//
// Pass a dns:/// target at a headless service to get every replica, for example
// dns:///epp-headless.llm-d.svc.cluster.local:9002.
func NewEPPClient(addr string) (*EPPClient, error) {
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
	return &EPPClient{conn: conn, client: extProcPb.NewExternalProcessorClient(conn)}, nil
}

func (c *EPPClient) Close() error { return c.conn.Close() }

// Ping reports whether EPP is reachable, for readiness. It asks the connection
// to leave idle and waits briefly for a usable state rather than opening an
// ext_proc stream, so probing costs nothing on EPP's side and cannot be
// mistaken for a request.
func (c *EPPClient) Ping(ctx context.Context) error {
	c.conn.Connect()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		switch s := c.conn.GetState(); s {
		case connectivity.Ready, connectivity.Idle:
			return nil
		case connectivity.Shutdown:
			return errors.New("connection shut down")
		default:
			if !c.conn.WaitForStateChange(ctx, s) {
				return fmt.Errorf("epp not reachable, state %s", s)
			}
		}
	}
}

// RouteResult is what a data plane needs back to act: where to send the
// request, what to change about it, or that it should not be sent at all.
type RouteResult struct {
	Destination   string
	SetHeaders    map[string]string
	RemoveHeaders []string
	// Body is non-nil when EPP returned a modified body. llm-d re-marshals
	// every OpenAI-parsed request, so this is the common case rather than the
	// exception, and forwarding the original instead would drop model rewrites.
	Body         []byte
	BodyModified bool
	Immediate    *Immediate
	Duration     time.Duration
}

type Immediate struct {
	Status  int
	Headers map[string]string
	Body    string
}

const destinationHeader = "x-gateway-destination-endpoint"

// Route runs one request through EPP and returns the decision. The caller keeps
// its own copy of the request; nothing about its connection or its client is
// EPP's business.
func (c *EPPClient) Route(ctx context.Context, headers map[string]string, body []byte) (*RouteResult, error) {
	start := time.Now()
	stream, err := c.client.Process(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.CloseSend() }()

	hm := &corev3.HeaderMap{}
	for k, v := range headers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, RawValue: []byte(v)})
	}
	if err := stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{Headers: hm},
		},
	}); err != nil {
		return nil, err
	}

	res := &RouteResult{SetHeaders: map[string]string{}}
	// EPP may answer headers before the body arrives, or hold everything until
	// end of stream. Both are legal, so read opportunistically after sending.
	if err := sendBodyChunks(stream, body); err != nil {
		return nil, err
	}

	var rebuilt bytes.Buffer
	for {
		resp, rerr := stream.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
		done, err := c.absorb(resp, res, &rebuilt)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
	}

	if rebuilt.Len() > 0 {
		res.Body = rebuilt.Bytes()
		res.BodyModified = !bytes.Equal(res.Body, body)
	}
	if d, ok := res.SetHeaders[destinationHeader]; ok {
		res.Destination = d
	}
	res.Duration = time.Since(start)
	return res, nil
}

// absorb folds one ext_proc response into the result and reports whether the
// exchange is finished.
func (c *EPPClient) absorb(resp *extProcPb.ProcessingResponse, res *RouteResult, body *bytes.Buffer) (bool, error) {
	if ir := resp.GetImmediateResponse(); ir != nil {
		res.Immediate = &Immediate{
			Status:  int(ir.GetStatus().GetCode()),
			Body:    string(ir.GetBody()),
			Headers: map[string]string{},
		}
		for _, h := range ir.GetHeaders().GetSetHeaders() {
			res.Immediate.Headers[h.GetHeader().GetKey()] = string(h.GetHeader().GetRawValue())
		}
		return true, nil
	}

	var common *extProcPb.CommonResponse
	isBody := false
	switch v := resp.Response.(type) {
	case *extProcPb.ProcessingResponse_RequestHeaders:
		common = v.RequestHeaders.GetResponse()
	case *extProcPb.ProcessingResponse_RequestBody:
		common = v.RequestBody.GetResponse()
		isBody = true
	default:
		return false, nil
	}
	if common == nil {
		return false, nil
	}

	if hm := common.GetHeaderMutation(); hm != nil {
		for _, h := range hm.GetSetHeaders() {
			val := string(h.GetHeader().GetRawValue())
			if val == "" {
				val = h.GetHeader().GetValue()
			}
			res.SetHeaders[h.GetHeader().GetKey()] = val
		}
		res.RemoveHeaders = append(res.RemoveHeaders, hm.GetRemoveHeaders()...)
	}

	if bm := common.GetBodyMutation(); bm != nil {
		if sr := bm.GetStreamedResponse(); sr != nil {
			body.Write(sr.GetBody())
			if sr.GetEndOfStream() {
				return true, nil
			}
			return false, nil
		}
		if b := bm.GetBody(); b != nil {
			body.Write(b)
			return true, nil
		}
	}
	// A body-phase response carrying no body mutation ends the request phase.
	return isBody, nil
}
