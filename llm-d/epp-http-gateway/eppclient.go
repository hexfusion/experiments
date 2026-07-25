package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// EPPClient speaks ext_proc to an unchanged EPP.
//
// Everything unpleasant about the protocol lives here: the phase sequence, the
// requirement to answer nothing but to receive everything in order, the body
// arriving back in 62KB chunks because llm-d runs FULL_DUPLEX_STREAMED, and the
// fact that a decision can arrive as a header mutation on either the headers or
// the body response.
type EPPClient struct {
	transport Transport
}

// NewEPPClientWithTransport lets a caller choose how the exchange travels.
// Everything below this line is transport-agnostic.
func NewEPPClientWithTransport(t Transport) *EPPClient {
	return &EPPClient{transport: t}
}

// NewEPPClient builds a client over the default transport.
func NewEPPClient(addr string) (*EPPClient, error) {
	t, err := NewGRPCTransport(addr)
	if err != nil {
		return nil, err
	}
	return &EPPClient{transport: t}, nil
}

func (c *EPPClient) Close() error { return c.transport.Close() }

// Ping reports whether EPP is reachable, for readiness.
func (c *EPPClient) Ping(ctx context.Context) error { return c.transport.Ping(ctx) }

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
	stream, err := c.transport.Open(ctx)
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
