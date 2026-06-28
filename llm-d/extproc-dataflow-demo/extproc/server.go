// Package extproc is the shared ext_proc plumbing for the demo. It hides the Envoy proto verbosity
// so the epp and ipp packages express only their logic.
//
// Envoy opens one gRPC stream per HTTP request per filter (envoy issue 35317). Serve builds a fresh
// StreamHandler per stream, so a component gets natural per-request state across phases.
package extproc

import (
	"io"
	"log"
	"net"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	filterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	ep "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
)

// Component is an ext_proc server (the EPP, or the IPP). It mints one StreamHandler per request.
type Component interface {
	NewStream() StreamHandler
}

// StreamHandler handles the phases of a single HTTP request on one gRPC stream.
type StreamHandler interface {
	OnRequestHeaders(reqID string, get HeaderGetter) Mutation
	OnRequestBody(body []byte) Mutation
}

// HeaderGetter looks up a request header (case-insensitive); "" if absent.
type HeaderGetter func(name string) string

// Mutation is what a hook asks Envoy to do. Zero value = pass through unchanged.
type Mutation struct {
	SetHeaders  map[string]string // headers to set on the request
	ReplaceBody []byte            // if non-nil, replace the request body (content-length is fixed)

	// WantBody (header phase) flips this filter into buffering the body via mode_override
	// (needs allow_mode_override): lets the post hook stay header-only until translation is needed.
	WantBody bool
}

// Serve runs an ext_proc server for component c on port. Blocks until the server stops.
func Serve(role, port string, c Component) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return err
	}
	s := grpc.NewServer()
	ep.RegisterExternalProcessorServer(s, &server{c: c})
	log.Printf("[%s] ext_proc listening :%s", role, port)
	return s.Serve(lis)
}

type server struct {
	c Component
	ep.UnimplementedExternalProcessorServer
}

func (s *server) Process(stream ep.ExternalProcessor_ProcessServer) error {
	sh := s.c.NewStream() // one StreamHandler per request
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var resp *ep.ProcessingResponse
		switch r := req.Request.(type) {
		case *ep.ProcessingRequest_RequestHeaders:
			get := getterFor(r.RequestHeaders.GetHeaders())
			resp = headerResponse(sh.OnRequestHeaders(get("x-request-id"), get))
		case *ep.ProcessingRequest_RequestBody:
			resp = bodyResponse(sh.OnRequestBody(r.RequestBody.GetBody()))
		default:
			resp = &ep.ProcessingResponse{}
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// --- proto translation -------------------------------------------------------

func getterFor(h *corev3.HeaderMap) HeaderGetter {
	return func(name string) string {
		if h == nil {
			return ""
		}
		for _, hv := range h.Headers {
			if strings.EqualFold(hv.Key, name) {
				if len(hv.RawValue) > 0 {
					return string(hv.RawValue)
				}
				return hv.Value
			}
		}
		return ""
	}
}

func setHeaderOpts(m map[string]string) []*corev3.HeaderValueOption {
	var opts []*corev3.HeaderValueOption
	for k, v := range m {
		opts = append(opts, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{Key: k, RawValue: []byte(v)},
		})
	}
	return opts
}

func headerResponse(m Mutation) *ep.ProcessingResponse {
	r := &ep.ProcessingResponse{
		Response: &ep.ProcessingResponse_RequestHeaders{
			RequestHeaders: &ep.HeadersResponse{
				Response: &ep.CommonResponse{
					HeaderMutation: &ep.HeaderMutation{SetHeaders: setHeaderOpts(m.SetHeaders)},
				},
			},
		},
	}
	if m.WantBody {
		// flip this filter into buffering the request body for the rest of the request
		r.ModeOverride = &filterv3.ProcessingMode{RequestBodyMode: filterv3.ProcessingMode_BUFFERED}
	}
	return r
}

func bodyResponse(m Mutation) *ep.ProcessingResponse {
	// header-only mutation in the body phase (e.g. set a header derived from the body), no replace
	if m.ReplaceBody == nil {
		common := &ep.CommonResponse{}
		if len(m.SetHeaders) > 0 {
			common.HeaderMutation = &ep.HeaderMutation{SetHeaders: setHeaderOpts(m.SetHeaders)}
		}
		return &ep.ProcessingResponse{
			Response: &ep.ProcessingResponse_RequestBody{RequestBody: &ep.BodyResponse{Response: common}},
		}
	}
	// replacing the body changes its length, so content-length must be corrected or Envoy 500s
	// (mismatch_between_content_length_and_the_length_of_the_mutated_body)
	headers := setHeaderOpts(m.SetHeaders)
	headers = append(headers, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: "content-length", RawValue: []byte(strconv.Itoa(len(m.ReplaceBody)))},
	})
	return &ep.ProcessingResponse{
		Response: &ep.ProcessingResponse_RequestBody{
			RequestBody: &ep.BodyResponse{
				Response: &ep.CommonResponse{
					Status:         ep.CommonResponse_CONTINUE_AND_REPLACE,
					HeaderMutation: &ep.HeaderMutation{SetHeaders: headers},
					BodyMutation:   &ep.BodyMutation{Mutation: &ep.BodyMutation_Body{Body: m.ReplaceBody}},
				},
			},
		},
	}
}

// Short trims a request id for readable logs.
func Short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
