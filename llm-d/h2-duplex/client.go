package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// ClientConfig streams a request body in chunks with a delay between them, so
// the request is still uploading while the response is expected to arrive.
type ClientConfig struct {
	Chunks     int
	ChunkBytes int
	ChunkDelay time.Duration
	Model      string
	H1         bool
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{Chunks: 12, ChunkBytes: 32 << 10, ChunkDelay: 20 * time.Millisecond, Model: "llama-3.1-8b"}
}

// BodyBytes is the total request size the config produces.
func (c ClientConfig) BodyBytes() int { return c.Chunks * c.ChunkBytes }

// Result is what the spike measures.
type Result struct {
	FirstResponseByte time.Duration
	LastRequestByte   time.Duration
	ResponseBytes     int
	Status            int
	Duplex            bool
}

func newClient(h1 bool) *http.Client {
	if h1 {
		return &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: false}}
	}
	return &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

// RunClient streams the body and records when the first response byte lands
// relative to when the last request byte was sent.
func RunClient(ctx context.Context, target string, cfg ClientConfig, tl *Timeline) (*Result, error) {
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"/v1/chat/completions", pr)
	if err != nil {
		return nil, err
	}
	req.ContentLength = -1
	req.Header.Set("content-type", "application/json")

	// The head carries the routing fields, so the policy service can parse once
	// off the first read without waiting for the rest.
	head := fmt.Sprintf(`{"model":"%s","stream":true,"messages":[{"role":"user","content":"`, cfg.Model)
	filler := strings.Repeat("x", cfg.ChunkBytes)

	go func() {
		var werr error
		defer func() { pw.CloseWithError(werr) }()
		if _, werr = io.WriteString(pw, head); werr != nil {
			return
		}
		tl.Mark("client.request-first-byte-sent")
		for i := 0; i < cfg.Chunks; i++ {
			if _, werr = io.WriteString(pw, filler); werr != nil {
				return
			}
			time.Sleep(cfg.ChunkDelay)
		}
		_, werr = io.WriteString(pw, `"}]}`)
		tl.Set("client.request-last-byte-sent")
	}()

	resp, err := newClient(cfg.H1).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	res := &Result{Status: resp.StatusCode}
	buf := make([]byte, 8192)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if res.ResponseBytes == 0 {
				res.FirstResponseByte = tl.Mark("client.response-first-byte-received")
			}
			res.ResponseBytes += n
		}
		if rerr != nil {
			break
		}
	}
	tl.Set("client.response-complete")

	res.FirstResponseByte, _ = tl.Get("client.response-first-byte-received")
	res.LastRequestByte, _ = tl.Get("client.request-last-byte-sent")
	res.Duplex = res.FirstResponseByte > 0 && res.FirstResponseByte < res.LastRequestByte
	return res, nil
}
