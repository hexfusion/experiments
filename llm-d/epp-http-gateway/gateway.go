package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Gateway is the EPP-facing edge: HTTP in from a data plane that does not want
// ext_proc, ext_proc out to an EPP that is unchanged.
//
// The data plane calls this the way it calls any other service. It does not
// implement a protocol, does not manage a bidirectional stream, and does not
// learn which body-mutation shape is legal in which processing mode.
type Gateway struct {
	epp *EPPClient

	routed   atomic.Int64
	shed     atomic.Int64
	rewrote  atomic.Int64
	failures atomic.Int64
	reported atomic.Int64
	aborted  atomic.Int64
}

func NewGateway(epp *EPPClient) *Gateway { return &Gateway{epp: epp} }

func (g *Gateway) Stats() (routed, shed, rewrote, failures int64) {
	return g.routed.Load(), g.shed.Load(), g.rewrote.Load(), g.failures.Load()
}

// ResponseStats reports completed and aborted response phases separately,
// because an aborted stream carries a partial token count that must not be
// mistaken for a real one.
func (g *Gateway) ResponseStats() (reported, aborted int64) {
	return g.reported.Load(), g.aborted.Load()
}

// decisionHeader carries the routing decision as JSON. The response body is the
// request body to forward, so a rewritten body needs no encoding and no copy
// beyond the one the HTTP stack already makes.
const decisionHeader = "X-Routing-Decision"

// Decision is the JSON a caller reads off the response header.
type Decision struct {
	Destination   string            `json:"destination,omitempty"`
	SetHeaders    map[string]string `json:"set_headers,omitempty"`
	RemoveHeaders []string          `json:"remove_headers,omitempty"`
	BodyModified  bool              `json:"body_modified"`
	EPPDurationMs float64           `json:"epp_duration_ms"`
}

// Route is the whole API. POST the request that needs a destination; the
// response body is the request body to forward, which may differ from what was
// sent because EPP re-marshals.
//
// Inbound headers are forwarded to EPP with an x-forwarded- prefix stripped, so
// a caller can pass the client's headers through verbatim.
func (g *Gateway) Route(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		g.failures.Add(1)
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	headers := map[string]string{
		":path":          headerOr(r, "x-original-path", "/v1/chat/completions"),
		":method":        headerOr(r, "x-original-method", "POST"),
		"content-type":   headerOr(r, "content-type", "application/json"),
		"content-length": itoa(len(body)),
	}
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-original-") || lk == "content-length" {
			continue
		}
		if len(vs) > 0 {
			headers[lk] = vs[0]
		}
	}

	res, err := g.epp.Route(r.Context(), headers, body)
	if err != nil {
		g.failures.Add(1)
		http.Error(w, "epp: "+err.Error(), http.StatusBadGateway)
		return
	}

	// EPP declined the request. Load shedding and admission control arrive this
	// way, and the caller should return it to the client rather than forward.
	if res.Immediate != nil {
		g.shed.Add(1)
		for k, v := range res.Immediate.Headers {
			w.Header().Set(k, v)
		}
		status := res.Immediate.Status
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, res.Immediate.Body)
		return
	}

	dec := Decision{
		Destination:   res.Destination,
		SetHeaders:    res.SetHeaders,
		RemoveHeaders: res.RemoveHeaders,
		BodyModified:  res.BodyModified,
		EPPDurationMs: float64(res.Duration.Microseconds()) / 1000,
	}
	raw, err := json.Marshal(dec)
	if err != nil {
		g.failures.Add(1)
		http.Error(w, "encode decision: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out := body
	if res.Body != nil {
		out = res.Body
	}
	if res.BodyModified {
		g.rewrote.Add(1)
	}
	g.routed.Add(1)

	w.Header().Set(decisionHeader, string(raw))
	w.Header().Set("content-type", headerOr(r, "content-type", "application/json"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (g *Gateway) Health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/route", g.Route)
	mux.HandleFunc("POST /v1/session", g.Session)
	mux.HandleFunc("GET /healthz", g.Health)
	return mux
}

func headerOr(r *http.Request, key, fallback string) string {
	if v := r.Header.Get(key); v != "" {
		return v
	}
	return fallback
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
