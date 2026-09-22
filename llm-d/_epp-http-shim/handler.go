package httpshim

import (
	"encoding/json"
	"io"
	"net/http"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/llm-d/llm-d-router/pkg/epp/metadata"
)

// Processor is the ext_proc entry point. *handlers.StreamingServer satisfies it
// with no changes.
type Processor interface {
	Process(srv extProcPb.ExternalProcessor_ProcessServer) error
}

// Decision is what a caller gets back. No phases, no processing modes, no
// stream: the shape says nothing about how EPP is implemented.
type Decision struct {
	Endpoint string            `json:"endpoint,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Denied   *Denial           `json:"denied,omitempty"`
}

type Denial struct {
	Code int    `json:"code"`
	Body string `json:"body,omitempty"`
}

// Handler serves the routing call. Mount it alongside the gRPC server on the
// same StreamingServer: existing ext_proc callers are unaffected, and both
// front doors run identical scheduling.
//
// It is a passthrough rather than a route of its own. Whatever path arrives is
// the path EPP sees, which matters because EPP resolves parsers by path suffix:
// a request presented as /v1/schedule matches no parser and is rejected. The
// caller sends the request it was already going to send, to the path it was
// already going to send it to, and gets a decision back instead of a completion.
type Handler struct {
	Proc      Processor
	MaxBody   int64
	AuthAudit func(*http.Request) error
}

var _ http.Handler = (*Handler)(nil)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.AuthAudit != nil {
		if err := h.AuthAudit(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}

	max := h.MaxBody
	if max <= 0 {
		max = 32 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, max))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dec, err := h.decide(r, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if dec.Denied != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(dec.Denied.Code)
		_ = json.NewEncoder(w).Encode(dec)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dec)
}

// decide drives one request phase and returns what EPP decided. The stream is a
// pair of channels, so this is a function call with a goroutine, not a hop.
//
// Concurrency contract, which is the part that has to be right at every request
// rate: the input is fully written and closed before anything is read back, so
// Process always reaches EOF and always returns. The output is then drained to
// completion rather than abandoned on the first useful response. Breaking out
// early would leave Process blocked on a full channel forever, leaking a
// goroutine per request.
func (h *Handler) decide(r *http.Request, body []byte) (*Decision, error) {
	s := newStream(r.Context())

	// Buffered wide enough for the whole request phase, so these never block.
	s.in <- &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{Headers: toHeaderMap(r)},
		},
	}
	s.in <- &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{Body: body, EndOfStream: true},
		},
	}
	close(s.in)

	done := make(chan error, 1)
	go func() {
		err := h.Proc.Process(s)
		close(s.out)
		done <- err
	}()

	dec := &Decision{Headers: map[string]string{}}
	for resp := range s.out {
		if imm := resp.GetImmediateResponse(); imm != nil {
			dec.Denied = &Denial{Code: int(imm.GetStatus().GetCode()), Body: string(imm.GetBody())}
			continue
		}
		for k, v := range setHeaders(resp) {
			dec.Headers[k] = v
			if k == metadata.DestinationEndpointKey && dec.Endpoint == "" {
				dec.Endpoint = v
			}
		}
	}

	// A decision plus a stream error is still a decision: Process reports EOF
	// through the same return value as a real failure.
	if err := <-done; err != nil && dec.Endpoint == "" && dec.Denied == nil {
		return nil, err
	}
	return dec, nil
}

func toHeaderMap(r *http.Request) *corev3.HeaderMap {
	hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
		{Key: ":method", RawValue: []byte(r.Method)},
		{Key: ":path", RawValue: []byte(r.URL.Path)},
	}}
	for k, vs := range r.Header {
		for _, v := range vs {
			hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, RawValue: []byte(v)})
		}
	}
	return hm
}

func setHeaders(resp *extProcPb.ProcessingResponse) map[string]string {
	out := map[string]string{}
	var common *extProcPb.CommonResponse
	switch {
	case resp.GetRequestHeaders() != nil:
		common = resp.GetRequestHeaders().GetResponse()
	case resp.GetRequestBody() != nil:
		common = resp.GetRequestBody().GetResponse()
	}
	for _, hv := range common.GetHeaderMutation().GetSetHeaders() {
		v := string(hv.GetHeader().GetRawValue())
		if v == "" {
			v = hv.GetHeader().GetValue()
		}
		out[hv.GetHeader().GetKey()] = v
	}
	return out
}
