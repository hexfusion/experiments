// metadata-test: settles one question on real Envoy ext_proc.
//
// Does a filter placed BEFORE the EPP receive, in its request-BODY phase, dynamic metadata that the
// EPP set in its request-HEADER phase? Envoy source (ext_proc.cc addDynamicMetadata, called fresh in
// setupBodyChunk) suggests yes; an earlier measurement saw the body phase empty. This isolates it.
//
// Chain: ipp (pre-EPP, body BUFFERED, forwards [envoy.lb, test.pick])
//        epp (sets envoy.lb + test.pick dynamic metadata, and an x-pick header)
//        probe (post-EPP, forwards the same namespaces)  <- control: proves the EPP set it
//        echo
//
// Read the logs:
//   [epp]        set metadata ...
//   [probe/hdr]  metadata ns=[...] envoy.lb.pick=... test.pick.value=...   <- control: should be populated
//   [ipp/body]   metadata ns=[...] envoy.lb.pick=... test.pick.value=...   <- the answer
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func main() {
	role := flag.String("role", "", "ipp|epp|probe|echo")
	port := flag.String("port", "", "listen port")
	flag.Parse()
	if *role == "echo" {
		runEcho(*port)
		return
	}
	lis, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	extproc.RegisterExternalProcessorServer(s, &server{role: *role})
	log.Printf("[%s] listening :%s", *role, *port)
	log.Fatal(s.Serve(lis))
}

type server struct {
	role string
	extproc.UnimplementedExternalProcessorServer
}

func (s *server) Process(stream extproc.ExternalProcessor_ProcessServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var resp *extproc.ProcessingResponse
		switch r := req.Request.(type) {
		case *extproc.ProcessingRequest_RequestHeaders:
			resp = s.onHeaders(req, r.RequestHeaders)
		case *extproc.ProcessingRequest_RequestBody:
			resp = s.onBody(req, r.RequestBody)
		default:
			resp = &extproc.ProcessingResponse{}
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func (s *server) onHeaders(req *extproc.ProcessingRequest, h *extproc.HttpHeaders) *extproc.ProcessingResponse {
	switch s.role {
	case "epp":
		pick := "azure.example:443"
		log.Printf("[epp]       set metadata envoy.lb.pick=%q test.pick.value=%q + header x-pick", pick, pick)
		resp := setHeader("x-pick", pick)
		resp.DynamicMetadata = &structpb.Struct{Fields: map[string]*structpb.Value{
			"envoy.lb":  structpb.NewStructValue(strct("x-gateway-destination-endpoint", pick)),
			"test.pick": structpb.NewStructValue(strct("value", pick)),
		}}
		return resp

	case "probe": // control: a filter AFTER the EPP, reading metadata in its HEADER phase
		log.Printf("[probe/hdr] %s", dump(req.MetadataContext))
		return cont()

	case "ipp": // pre-EPP: header phase is before the EPP, so expected empty here
		log.Printf("[ipp/hdr]   (pre-EPP) %s", dump(req.MetadataContext))
		return cont() // continue so the buffered body phase fires; body is the measurement
	}
	return cont()
}

// cont is a proper "continue, no mutation" headers response so Envoy proceeds to the body phase.
func cont() *extproc.ProcessingResponse {
	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extproc.HeadersResponse{Response: &extproc.CommonResponse{}},
		},
	}
}

func (s *server) onBody(req *extproc.ProcessingRequest, b *extproc.HttpBody) *extproc.ProcessingResponse {
	if s.role == "ipp" { // THE ANSWER: does the pre-EPP filter's body phase see the EPP's metadata?
		log.Printf("[ipp/body]  (post-EPP chronologically) %s", dump(req.MetadataContext))
	}
	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_RequestBody{RequestBody: &extproc.BodyResponse{}},
	}
}

// --- helpers -----------------------------------------------------------------

func strct(k, v string) *structpb.Struct {
	return &structpb.Struct{Fields: map[string]*structpb.Value{k: structpb.NewStringValue(v)}}
}

// dump renders the metadata_context: the namespaces present, and the two values we care about.
func dump(m *corev3.Metadata) string {
	if m == nil {
		return "metadata_context=nil"
	}
	var ns []string
	for k := range m.FilterMetadata {
		ns = append(ns, k)
	}
	sort.Strings(ns)
	return fmt.Sprintf("metadata ns=%v  envoy.lb.pick=%q  test.pick.value=%q",
		ns, field(m, "envoy.lb", "x-gateway-destination-endpoint"), field(m, "test.pick", "value"))
}

func field(m *corev3.Metadata, ns, key string) string {
	s := m.FilterMetadata[ns]
	if s == nil {
		return ""
	}
	if v := s.Fields[key]; v != nil {
		return v.GetStringValue()
	}
	return ""
}

func setHeader(k, v string) *extproc.ProcessingResponse {
	return &extproc.ProcessingResponse{
		Response: &extproc.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extproc.HeadersResponse{
				Response: &extproc.CommonResponse{
					HeaderMutation: &extproc.HeaderMutation{
						SetHeaders: []*corev3.HeaderValueOption{
							{Header: &corev3.HeaderValue{Key: k, RawValue: []byte(v)}},
						},
					},
				},
			},
		},
	}
}

func runEcho(port string) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok\n"))
		log.Printf("[echo] %s %s x-pick=%s", r.Method, r.URL.Path, r.Header.Get("x-pick"))
	})
	log.Printf("[echo] listening :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

var _ = strings.TrimSpace
