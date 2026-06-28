// Package ipp brackets the EPP with two hooks (one process): pre extracts the model from the body
// (BUFFERED) before the EPP; post injects the destination's token (header-only) after it, and only
// buffers the body to translate when the destination speaks a different API.
package ipp

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/hexfusion/experiments/llm-d/extproc-dataflow-demo/extproc"
)

type Server struct {
	mu  sync.Mutex
	ctx map[string]reqCtx // pre->post handoff, keyed by x-request-id; entitlement never leaves here
}

type reqCtx struct {
	model       string
	entitlement string
}

func New() *Server { return &Server{ctx: map[string]reqCtx{}} }

func (s *Server) remember(id string, c reqCtx) {
	s.mu.Lock()
	s.ctx[id] = c
	s.mu.Unlock()
}

func (s *Server) recall(id string) (reqCtx, bool) {
	s.mu.Lock()
	c, ok := s.ctx[id]
	delete(s.ctx, id)
	s.mu.Unlock()
	return c, ok
}

func (s *Server) NewStream() extproc.StreamHandler { return &stream{srv: s} }

type stream struct {
	srv         *Server
	reqID       string
	entitlement string // pre hook
	provider    string // post hook; non-empty marks this stream as post
	translate   bool   // post hook
}

func (st *stream) OnRequestHeaders(reqID string, get extproc.HeaderGetter) extproc.Mutation {
	st.reqID = reqID
	// Only the post hook sees the pick header (the EPP ran before it).
	if dst := get("x-gateway-destination-endpoint"); dst != "" {
		return st.post(reqID, dst)
	}
	tenant := get("x-tenant")
	if tenant == "" {
		tenant = "anonymous"
	}
	st.entitlement = tenant + ":gold"
	return extproc.Mutation{} // model is in the body; continue to the body phase
}

func (st *stream) post(reqID, dst string) extproc.Mutation {
	c, _ := st.srv.recall(reqID)
	p := providerOf(dst)
	st.provider, st.translate = p.name, !p.sameAPI
	m := extproc.Mutation{SetHeaders: map[string]string{
		"authorization":  fmt.Sprintf("Bearer %s/%s", p.keyPrefix, c.entitlement),
		"x-ipp-provider": p.name,
	}}
	if st.translate {
		m.WantBody = true // mode_override: buffer the body to translate it
		log.Printf("[ipp/post] reqid=%s -> %s: inject auth + TRANSLATE (buffers body)", extproc.Short(reqID), p.name)
	} else {
		log.Printf("[ipp/post] reqid=%s -> %s: inject auth only (header, no body buffer)", extproc.Short(reqID), p.name)
	}
	return m
}

func (st *stream) OnRequestBody(body []byte) extproc.Mutation {
	if st.provider != "" { // post hook (only reached when translation flipped buffering on)
		out := translateToAnthropic(body)
		log.Printf("[ipp/post] reqid=%s translate %dB -> %dB (OpenAI -> Anthropic)", extproc.Short(st.reqID), len(body), len(out))
		return extproc.Mutation{ReplaceBody: out}
	}
	// pre hook: extract the model from the real body, stash state, set the routing header
	model := extractModel(body)
	st.srv.remember(st.reqID, reqCtx{model: model, entitlement: st.entitlement})
	log.Printf("[ipp/pre]  reqid=%s model=%q from body (%dB); entitlement kept server-side", extproc.Short(st.reqID), model, len(body))
	return extproc.Mutation{SetHeaders: map[string]string{"x-gateway-model-name": model}}
}

// --- providers ---------------------------------------------------------------

type providerInfo struct {
	name      string
	keyPrefix string
	sameAPI   bool
}

func providerOf(dst string) providerInfo {
	switch strings.SplitN(dst, ".", 2)[0] {
	case "anthropic":
		return providerInfo{"anthropic", "sk-ant", false}
	default:
		return providerInfo{"openai", "sk-openai", true} // OpenAI-compatible
	}
}

func extractModel(body []byte) string {
	var r struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &r) != nil || r.Model == "" {
		return "unknown"
	}
	return r.Model
}

// translateToAnthropic: simplified OpenAI chat.completions -> Anthropic /v1/messages remap.
func translateToAnthropic(body []byte) []byte {
	var in struct {
		Model     string           `json:"model"`
		Messages  []map[string]any `json:"messages"`
		MaxTokens int              `json:"max_tokens"`
	}
	_ = json.Unmarshal(body, &in)
	mt := in.MaxTokens
	if mt == 0 {
		mt = 1024
	}
	b, _ := json.Marshal(map[string]any{
		"model":             in.Model,
		"max_tokens":        mt,
		"anthropic_version": "2023-06-01",
		"messages":          in.Messages,
		"_translated_from":  "openai/chat.completions",
	})
	return b
}
