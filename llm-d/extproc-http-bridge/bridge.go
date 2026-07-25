package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

const chunkLimit = 62000

// Bridge speaks ext_proc toward the gateway and plain HTTP toward plugins.
//
// It exists so plugin authors never touch the ext_proc protocol. Everything
// that made ext_proc painful stays here: the phase state machine, the
// processing-mode rules about which body mutation is legal in which mode, the
// chunking, and the requirement to answer every message. A plugin is an HTTP
// handler that receives metadata and returns a decision.
type Bridge struct {
	extProcPb.UnimplementedExternalProcessorServer

	plugins []PluginSpec
	client  *http.Client
	extract Extractor

	// mode is derived from what the plugins declared, not configured by hand.
	returnBody bool

	requests atomic.Int64
	denied   atomic.Int64
}

func NewBridge(plugins []PluginSpec, extract Extractor) *Bridge {
	b := &Bridge{
		plugins: plugins,
		extract: extract,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        256,
				MaxIdleConnsPerHost: 256,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	// The body only goes back to the gateway if something declared it will
	// mutate. Nothing declaring mutation is the common case and it is free.
	for _, p := range plugins {
		if p.MutatesRequest {
			b.returnBody = true
		}
	}
	return b
}

func (b *Bridge) Stats() (int64, int64) { return b.requests.Load(), b.denied.Load() }

type reqState struct {
	headers   map[string]string
	path      string
	method    string
	requestID string
	body      []byte
	meta      *Metadata
	decoder   *SSEDecoder
	status    int
	streamed  bool
	completed bool
}

func (b *Bridge) Process(stream extProcPb.ExternalProcessor_ProcessServer) error {
	st := &reqState{headers: map[string]string{}}
	ctx := stream.Context()

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			b.finish(ctx, st)
			return nil
		}
		if err != nil {
			b.finish(ctx, st)
			return err
		}

		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			b.readHeaders(st, v.RequestHeaders.GetHeaders())
			// Every message must be answered or the gateway stalls.
			if err := send(stream, &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extProcPb.HeadersResponse{Response: &extProcPb.CommonResponse{}},
				},
			}); err != nil {
				return err
			}

		case *extProcPb.ProcessingRequest_RequestBody:
			st.body = append(st.body, v.RequestBody.GetBody()...)
			if !v.RequestBody.GetEndOfStream() {
				continue
			}
			resp, err := b.onRequestComplete(ctx, st)
			if err != nil {
				return err
			}
			if err := send(stream, resp); err != nil {
				return err
			}
			st.body = nil

		case *extProcPb.ProcessingRequest_RequestTrailers:
			if err := send(stream, &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestTrailers{
					RequestTrailers: &extProcPb.TrailersResponse{},
				},
			}); err != nil {
				return err
			}

		case *extProcPb.ProcessingRequest_ResponseHeaders:
			for _, h := range v.ResponseHeaders.GetHeaders().GetHeaders() {
				val := string(h.RawValue)
				switch h.Key {
				case ":status":
					st.status, _ = strconv.Atoi(val)
				case "content-type":
					if bytes.Contains([]byte(val), []byte("text/event-stream")) {
						st.streamed = true
					}
				}
			}
			if err := send(stream, &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_ResponseHeaders{
					ResponseHeaders: &extProcPb.HeadersResponse{Response: &extProcPb.CommonResponse{}},
				},
			}); err != nil {
				return err
			}

		case *extProcPb.ProcessingRequest_ResponseBody:
			if st.decoder == nil {
				st.decoder = &SSEDecoder{}
			}
			_ = st.decoder.Decode(v.ResponseBody.GetBody())
			if v.ResponseBody.GetEndOfStream() {
				st.completed = true
			}
			// In STREAMED mode a plain empty CommonResponse leaves the chunk
			// alone. A StreamedResponse mutation is only legal under
			// FULL_DUPLEX_STREAMED and stalls the stream here.
			if err := send(stream, &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_ResponseBody{
					ResponseBody: &extProcPb.BodyResponse{Response: &extProcPb.CommonResponse{}},
				},
			}); err != nil {
				return err
			}
		}
	}
}

func (b *Bridge) readHeaders(st *reqState, hm *corev3.HeaderMap) {
	for _, h := range hm.GetHeaders() {
		val := string(h.RawValue)
		if val == "" {
			val = h.Value
		}
		st.headers[h.Key] = val
		switch h.Key {
		case ":path":
			st.path = val
		case ":method":
			st.method = val
		case "x-request-id":
			st.requestID = val
		}
	}
}

// onRequestComplete is the single materialization point: extract once, then run
// every plugin against metadata.
func (b *Bridge) onRequestComplete(ctx context.Context, st *reqState) (*extProcPb.ProcessingResponse, error) {
	b.requests.Add(1)

	meta := &Metadata{
		RequestID: st.requestID,
		Method:    st.method,
		Path:      st.path,
		Headers:   st.headers,
		BodyBytes: len(st.body),
	}
	if b.extract != nil {
		if err := b.extract.Extract(ctx, st.body, meta); err != nil {
			return immediate(http.StatusBadRequest, "extract: "+err.Error()), nil
		}
	}
	st.meta = meta

	setHeaders := map[string]string{}
	removeHeaders := []string{}
	for _, p := range b.plugins {
		dec, err := b.call(ctx, p.URL+"/decide", meta)
		if err != nil {
			return immediate(http.StatusBadGateway, p.Name+": "+err.Error()), nil
		}
		if dec.Immediate != nil {
			b.denied.Add(1)
			return immediateFrom(dec.Immediate), nil
		}
		for k, v := range dec.SetHeaders {
			setHeaders[k] = v
		}
		removeHeaders = append(removeHeaders, dec.RemoveHeaders...)
		// Published attributes are visible to later plugins in the chain.
		for k, v := range dec.Publish {
			if meta.Attributes == nil {
				meta.Attributes = map[string]any{}
			}
			meta.Attributes[p.Name+"."+k] = v
		}
	}

	common := &extProcPb.CommonResponse{HeaderMutation: mutation(setHeaders, removeHeaders)}
	if b.returnBody {
		// Only when something declared it mutates. Otherwise the gateway keeps
		// the copy it already has and nothing crosses the wire.
		common.BodyMutation = &extProcPb.BodyMutation{
			Mutation: &extProcPb.BodyMutation_Body{Body: st.body},
		}
	}
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestBody{
			RequestBody: &extProcPb.BodyResponse{Response: common},
		},
	}, nil
}

// finish delivers response metadata to subscribers once, after the response
// completes, rather than per chunk.
func (b *Bridge) finish(ctx context.Context, st *reqState) {
	if st.meta == nil {
		return
	}
	rm := &ResponseMetadata{
		RequestID: st.meta.RequestID,
		Status:    st.status,
		Streamed:  st.streamed,
		Complete:  st.completed,
	}
	if st.decoder != nil {
		rm.Events = st.decoder.Events
		rm.Usage = st.decoder.Usage
	}
	for _, p := range b.plugins {
		if !p.WantsResponse {
			continue
		}
		_, _ = b.callResponse(ctx, p.URL+"/response", rm)
	}
}

func (b *Bridge) call(ctx context.Context, url string, meta *Metadata) (*Decision, error) {
	body, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("plugin status " + strconv.Itoa(resp.StatusCode))
	}
	out := &Decision{}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *Bridge) callResponse(ctx context.Context, url string, rm *ResponseMetadata) (*Decision, error) {
	body, err := json.Marshal(rm)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return &Decision{}, nil
}

func mutation(set map[string]string, remove []string) *extProcPb.HeaderMutation {
	if len(set) == 0 && len(remove) == 0 {
		return nil
	}
	hm := &extProcPb.HeaderMutation{RemoveHeaders: remove}
	for k, v := range set {
		hm.SetHeaders = append(hm.SetHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{Key: k, RawValue: []byte(v)},
		})
	}
	return hm
}

func immediate(status int, msg string) *extProcPb.ProcessingResponse {
	return immediateFrom(&ImmediateResponse{Status: status, Body: msg})
}

func immediateFrom(ir *ImmediateResponse) *extProcPb.ProcessingResponse {
	resp := &extProcPb.ImmediateResponse{
		Status: &typev3.HttpStatus{Code: typev3.StatusCode(ir.Status)},
		Body:   []byte(ir.Body),
	}
	if len(ir.Headers) > 0 {
		resp.Headers = mutation(ir.Headers, nil)
	}
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_ImmediateResponse{ImmediateResponse: resp},
	}
}

func send(stream extProcPb.ExternalProcessor_ProcessServer, r *extProcPb.ProcessingResponse) error {
	return stream.Send(r)
}
