// Package epp is the Endpoint Picker. It reads the model (which the IPP pre hook extracted from the
// body) and picks a destination. It holds no credential and does no payload mutation.
package epp

import (
	"log"

	"github.com/hexfusion/experiments/llm-d/extproc-dataflow-demo/extproc"
)

type Server struct{}

func New() *Server { return &Server{} }

func (s *Server) NewStream() extproc.StreamHandler { return &stream{} }

type stream struct{}

func (st *stream) OnRequestHeaders(reqID string, get extproc.HeaderGetter) extproc.Mutation {
	model := get("x-gateway-model-name") // set by the IPP pre hook from the request body
	dst := pick(model)
	log.Printf("[epp]      reqid=%s model=%q -> pick=%s", extproc.Short(reqID), model, dst)
	return extproc.Mutation{SetHeaders: map[string]string{"x-gateway-destination-endpoint": dst}}
}

func (st *stream) OnRequestBody(body []byte) extproc.Mutation { return extproc.Mutation{} }

// pick maps the model to a destination. The real EPP runs Filter/Score/Pick over candidate
// endpoints; a fixed model->destination map is enough to give each request a checkable route to a
// provider that is either OpenAI-compatible (same API, no translation) or different (Anthropic).
func pick(model string) string {
	switch model {
	case "claude-3-5-sonnet", "claude-3":
		return "anthropic.example:443" // different API -> the IPP post hook translates
	default:
		return "openai.example:443" // OpenAI-compatible -> no translation, auth only
	}
}
