package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// PolicyConfig is the next-hop policy service: an HTTP server toward the
// gateway and an HTTP client toward the model server, with the forward owned
// here rather than returned into a filter chain.
type PolicyConfig struct {
	Upstream  string
	PeekBytes int
	CopyBuf   int
	// DelayFirstWrite defers the first write to the upstream request body,
	// exercising golang/go#17480 (h2 transport hang on an unwritten pipe).
	DelayFirstWrite time.Duration
	H1              bool
}

func DefaultPolicyConfig(upstream string) PolicyConfig {
	return PolicyConfig{Upstream: upstream, PeekBytes: 2048, CopyBuf: 16 << 10}
}

// Metadata is what the parse stage extracts. Everything downstream decides from
// this; nothing downstream sees the body.
type Metadata struct {
	Model       string
	PeekedBytes int
}

// Policy holds the client and the high-water instrumentation.
type Policy struct {
	cfg    PolicyConfig
	client *http.Client
	// highWater is the largest number of body bytes held at once, across all
	// requests. It should stay flat as body size grows.
	highWater atomic.Int64
}

func NewPolicy(cfg PolicyConfig) *Policy {
	var rt http.RoundTripper
	if cfg.H1 {
		rt = &http.Transport{ForceAttemptHTTP2: false}
	} else {
		rt = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		}
	}
	return &Policy{cfg: cfg, client: &http.Client{Transport: rt}}
}

func (p *Policy) HighWater() int64 { return p.highWater.Load() }

func (p *Policy) recordHighWater(n int64) {
	for {
		cur := p.highWater.Load()
		if n <= cur || p.highWater.CompareAndSwap(cur, n) {
			return
		}
	}
}

// extractMetadata scans the peeked head only. A real parser does more, but it
// must not need bytes beyond what it was handed.
func extractMetadata(head []byte) Metadata {
	m := Metadata{PeekedBytes: len(head)}
	key := []byte(`"model"`)
	i := bytes.Index(head, key)
	if i < 0 {
		return m
	}
	rest := head[i+len(key):]
	if j := bytes.IndexByte(rest, ':'); j >= 0 {
		rest = rest[j+1:]
	}
	if j := bytes.IndexByte(rest, '"'); j >= 0 {
		rest = rest[j+1:]
		if k := bytes.IndexByte(rest, '"'); k >= 0 {
			m.Model = string(rest[:k])
		}
	}
	return m
}

func (p *Policy) Handler(tl *Timeline) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		tl.Mark("policy.headers-received")

		// Stage 1: parse once, off the head only. The rest of the body is never
		// materialized here.
		head := make([]byte, p.cfg.PeekBytes)
		n, err := io.ReadFull(r.Body, head)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			http.Error(w, "peek: "+err.Error(), http.StatusBadRequest)
			return
		}
		head = head[:n]
		meta := extractMetadata(head)
		tl.Mark("policy.parsed")
		p.recordHighWater(int64(len(head) + p.cfg.CopyBuf))

		// Stage 2: forward. The policy service owns the next hop, so it opens the
		// upstream request itself instead of returning a decision to a filter.
		pr, pw := io.Pipe()
		req, err := http.NewRequestWithContext(r.Context(), r.Method, p.cfg.Upstream+r.URL.Path, pr)
		if err != nil {
			http.Error(w, "build: "+err.Error(), http.StatusInternalServerError)
			return
		}
		req.ContentLength = -1
		if ct := r.Header.Get("content-type"); ct != "" {
			req.Header.Set("content-type", ct)
		}
		// The routing decision travels as metadata, not as the body.
		req.Header.Set("x-routing-model", meta.Model)

		go func() {
			if p.cfg.DelayFirstWrite > 0 {
				time.Sleep(p.cfg.DelayFirstWrite)
			}
			var werr error
			if _, werr = pw.Write(head); werr == nil {
				tl.Mark("policy.upstream-body-first-write")
				_, werr = io.CopyBuffer(pw, r.Body, make([]byte, p.cfg.CopyBuf))
			}
			tl.Set("policy.upstream-body-complete")
			pw.CloseWithError(werr)
		}()

		// Do returns on upstream response headers, not on body completion. That
		// return happening while the goroutine above is still writing is the
		// property under test.
		resp, err := p.client.Do(req)
		if err != nil {
			http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		tl.Mark("policy.upstream-response-headers")

		// Stage 3: relay downstream while the request body may still be uploading.
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		buf := make([]byte, p.cfg.CopyBuf)
		for {
			rn, rerr := resp.Body.Read(buf)
			if rn > 0 {
				if _, werr := w.Write(buf[:rn]); werr != nil {
					return
				}
				flusher.Flush()
				tl.Mark("policy.first-response-byte-relayed")
			}
			if rerr != nil {
				break
			}
		}
		tl.Set("policy.response-complete")
	})
}

// StartH2C serves h so that plaintext HTTP/2 is accepted, which is the in-cluster
// gateway-to-service shape. Returns the address and a stop func.
func StartH2C(h http.Handler) (string, func(), error) {
	return startServer(h, false)
}

// StartH1 serves h over HTTP/1.1 only, as the control case.
func StartH1(h http.Handler) (string, func(), error) {
	return startServer(h, true)
}

func startServer(h http.Handler, h1 bool) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	handler := h
	if !h1 {
		handler = h2c.NewHandler(h, &http2.Server{})
	} else {
		// HTTP/1 needs an explicit opt-in to interleave body reads with response
		// writes; HTTP/2 always permits it.
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rc := http.NewResponseController(w); rc != nil {
				_ = rc.EnableFullDuplex()
			}
			h.ServeHTTP(w, r)
		})
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return fmt.Sprintf("http://%s", ln.Addr().String()), stop, nil
}
