package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// RenderConfig models vLLM's render endpoint as a separate service, which is
// what it is: EPP POSTs the whole request body to it synchronously on the hot
// path and gets token ids back.
//
// Concurrency is the parameter that matters. The real endpoint is Python and
// serialises on the GIL, so it saturates at a low concurrent-call count rather
// than at CPU. Everything above that queues.
type RenderConfig struct {
	Concurrency int
	// PerCallLatency is fixed overhead per call, on top of the size-dependent
	// tokenization work.
	PerCallLatency time.Duration
}

func DefaultRenderConfig() RenderConfig {
	return RenderConfig{Concurrency: 8, PerCallLatency: 2 * time.Millisecond}
}

// RenderService is the stand-in. It does real work proportional to body size so
// that sharing one call is measurably different from making three.
type RenderService struct {
	sem   chan struct{}
	cfg   RenderConfig
	calls atomic.Int64
	bytes atomic.Int64
}

func NewRenderService(cfg RenderConfig) *RenderService {
	return &RenderService{sem: make(chan struct{}, cfg.Concurrency), cfg: cfg}
}

func (r *RenderService) Calls() (int64, int64) { return r.calls.Load(), r.bytes.Load() }

func (r *RenderService) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.calls.Add(1)
	r.bytes.Add(int64(len(body)))

	// Bounded concurrency: the GIL shelf.
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	if r.cfg.PerCallLatency > 0 {
		time.Sleep(r.cfg.PerCallLatency)
	}

	// The service parses and tokenizes, which is the work being amortized.
	var chat ChatRequest
	if err := json.Unmarshal(body, &chat); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m := metadataFromRequest(&chat)

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(renderResponse{
		TokenCount: m.TokenCount,
		Tokens:     m.Tokens,
		BlockKeys:  m.BlockKeys,
	})
}

type renderResponse struct {
	TokenCount int      `json:"token_count"`
	Tokens     []uint32 `json:"tokens"`
	BlockKeys  []uint64 `json:"block_keys"`
}

func StartRenderService(port int, cfg RenderConfig) (*RenderService, func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return nil, nil, err
	}
	svc := NewRenderService(cfg)
	srv := &http.Server{Handler: svc}
	go func() { _ = srv.Serve(ln) }()
	return svc, func() { _ = srv.Close() }, nil
}

// RenderClient matches how llm-d dials render: HTTP/1.1 only, because vLLM does
// not speak h2 here, with a bounded idle pool.
type RenderClient struct {
	url    string
	client *http.Client
}

func NewRenderClient(url string) *RenderClient {
	return &RenderClient{
		url: url,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 16,
				TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
			},
		},
	}
}

// Render POSTs the whole body and returns the tokenization. This is the call
// single ownership exists to make once instead of once per consumer.
func (c *RenderClient) Render(ctx context.Context, body []byte) (*renderResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/v1/chat/completions/render", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := &renderResponse{}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}
