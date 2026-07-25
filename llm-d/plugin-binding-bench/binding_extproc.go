package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// bodyChunkLimit matches what llm-d-router uses.
const bodyChunkLimit = 62000

const decisionHeader = "x-decision-endpoint"

// PayloadMode is what actually crosses the wire to the hosted plugin.
type PayloadMode int

const (
	// PayloadFullBody is the status quo: the plugin receives the whole request
	// body, parses it itself, and the body is echoed back as a mutation, which
	// FULL_DUPLEX_STREAMED requires.
	PayloadFullBody PayloadMode = iota
	// PayloadMetadata is the adapter: the shim parsed once and sends only the
	// extracted metadata. No body mutation comes back.
	PayloadMetadata
	// PayloadMetadataNoTokens is the adapter with the token sequence withheld,
	// which is only correct for plugins that do not score on prefix blocks.
	PayloadMetadataNoTokens
	// PayloadMetadataBinary carries the same metadata as PayloadMetadata,
	// including tokens, in a fixed-width binary encoding instead of JSON.
	PayloadMetadataBinary
	// PayloadFullBodyNoEcho passes the body, because some consumers still need
	// it, but declares no mutation so it is not returned. Isolates the echo cost
	// from the parse cost.
	PayloadFullBodyNoEcho
)

func (m PayloadMode) String() string {
	switch m {
	case PayloadFullBody:
		return "full-body"
	case PayloadMetadata:
		return "metadata"
	case PayloadMetadataNoTokens:
		return "metadata-no-tokens"
	case PayloadMetadataBinary:
		return "metadata-binary"
	case PayloadFullBodyNoEcho:
		return "full-body-no-echo"
	}
	return "unknown"
}

// extProcServer hosts one plugin behind the real ext_proc protobuf contract.
type extProcServer struct {
	extProcPb.UnimplementedExternalProcessorServer
	plugin Plugin
	mode   PayloadMode
}

func (s *extProcServer) Process(stream extProcPb.ExternalProcessor_ProcessServer) error {
	var buf []byte
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		body := req.GetRequestBody()
		if body == nil {
			continue
		}
		buf = append(buf, body.GetBody()...)
		if !body.GetEndOfStream() {
			continue
		}

		var dec Decision
		switch s.mode {
		case PayloadFullBody, PayloadFullBodyNoEcho:
			// The plugin has to do its own parse, because all it got was bytes.
			sc, ok := s.plugin.(*scorer)
			if !ok {
				return fmt.Errorf("full-body mode needs a parsing plugin")
			}
			d, perr := sc.ParseBodyThenDecide(buf)
			if perr != nil {
				return perr
			}
			dec = d
		case PayloadMetadataBinary:
			m, derr := DecodeBinary(buf)
			if derr != nil {
				return derr
			}
			dec = s.plugin.Decide(m)
		default:
			m, derr := DecodeMetadata(buf)
			if derr != nil {
				return derr
			}
			dec = s.plugin.Decide(m)
		}

		common := &extProcPb.CommonResponse{
			HeaderMutation: &extProcPb.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{{
					Header: &corev3.HeaderValue{Key: decisionHeader, RawValue: []byte(dec.Endpoint)},
				}},
			},
		}
		if s.mode == PayloadFullBody {
			// FULL_DUPLEX_STREAMED requires a StreamedBodyResponse, so the whole
			// body goes back out. This is the cost the adapter removes.
			if err := sendChunkedBody(stream, buf, common); err != nil {
				return err
			}
		} else {
			if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestBody{
					RequestBody: &extProcPb.BodyResponse{Response: common},
				},
			}); err != nil {
				return err
			}
		}
		buf = buf[:0]
	}
}

func sendChunkedBody(stream extProcPb.ExternalProcessor_ProcessServer, body []byte, first *extProcPb.CommonResponse) error {
	for start := 0; start < len(body); start += bodyChunkLimit {
		end := min(start+bodyChunkLimit, len(body))
		common := &extProcPb.CommonResponse{}
		if start == 0 && first != nil {
			common.HeaderMutation = first.HeaderMutation
		}
		common.BodyMutation = &extProcPb.BodyMutation{
			Mutation: &extProcPb.BodyMutation_StreamedResponse{
				StreamedResponse: &extProcPb.StreamedBodyResponse{
					Body:        body[start:end],
					EndOfStream: end >= len(body),
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
	}
	return nil
}

// StartExtProcPlugin runs a hosted plugin over loopback gRPC and returns its
// address plus a stop func.
func StartExtProcPlugin(p Plugin, mode PayloadMode) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, &extProcServer{plugin: p, mode: mode})
	go func() { _ = s.Serve(ln) }()
	return ln.Addr().String(), s.Stop, nil
}

// extProcBinding is the client side. One long-lived connection, one stream per
// request, which is what an ext_proc filter does.
type extProcBinding struct {
	name      string
	mode      PayloadMode
	conn      *grpc.ClientConn
	client    extProcPb.ExternalProcessorClient
	stop      func()
	wireBytes atomic.Int64
}

func NewExtProcBinding(p Plugin, mode PayloadMode) (Binding, error) {
	addr, stop, err := StartExtProcPlugin(p, mode)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		stop()
		return nil, err
	}
	return &extProcBinding{
		name:   "extproc-" + mode.String(),
		mode:   mode,
		conn:   conn,
		client: extProcPb.NewExternalProcessorClient(conn),
		stop:   stop,
	}, nil
}

func (b *extProcBinding) Name() string     { return b.name }
func (b *extProcBinding) WireBytes() int64 { return b.wireBytes.Load() }

func (b *extProcBinding) Close() error {
	err := b.conn.Close()
	b.stop()
	return err
}

func (b *extProcBinding) Invoke(ctx context.Context, m *Metadata, body []byte) (Decision, error) {
	payload := body
	switch b.mode {
	case PayloadFullBody, PayloadFullBodyNoEcho:
	case PayloadMetadataBinary:
		payload = m.EncodeBinary()
	default:
		enc, err := m.Encode(b.mode == PayloadMetadata)
		if err != nil {
			return Decision{}, err
		}
		payload = enc
	}

	stream, err := b.client.Process(ctx)
	if err != nil {
		return Decision{}, err
	}
	defer func() { _ = stream.CloseSend() }()

	sent := int64(0)
	for start := 0; start < len(payload); start += bodyChunkLimit {
		end := min(start+bodyChunkLimit, len(payload))
		if err := stream.Send(&extProcPb.ProcessingRequest{
			Request: &extProcPb.ProcessingRequest_RequestBody{
				RequestBody: &extProcPb.HttpBody{
					Body:        payload[start:end],
					EndOfStream: end >= len(payload),
				},
			},
		}); err != nil {
			return Decision{}, err
		}
		sent += int64(end - start)
	}

	var dec Decision
	got := false
	received := int64(0)
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Decision{}, err
		}
		rb := resp.GetRequestBody()
		if rb == nil {
			continue
		}
		common := rb.GetResponse()
		if hm := common.GetHeaderMutation(); hm != nil {
			for _, h := range hm.GetSetHeaders() {
				if h.GetHeader().GetKey() == decisionHeader {
					dec.Endpoint = string(h.GetHeader().GetRawValue())
					got = true
				}
			}
		}
		if sr := common.GetBodyMutation().GetStreamedResponse(); sr != nil {
			received += int64(len(sr.GetBody()))
			if sr.GetEndOfStream() {
				break
			}
			continue
		}
		if got {
			break
		}
	}
	b.wireBytes.Add(sent + received)
	if !got {
		return Decision{}, errors.New("no decision returned")
	}
	return dec, nil
}
