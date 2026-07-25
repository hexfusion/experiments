package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// ParseOnce is the single materialization and parse the whole design rests on.
func ParseOnce(body []byte) (*Metadata, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return MetadataFromRequest(&req), nil
}

// Frontend is Etai's shape: the shim is just an HTTP service. No Envoy, no
// ext_proc filter. It reads the request, parses once, calls its hosted plugins,
// and forwards upstream itself.
type Frontend struct {
	hosted   []Binding
	upstream string
	client   *http.Client
}

func NewFrontend(upstream string, hosted []Binding) *Frontend {
	return &Frontend{
		hosted:   hosted,
		upstream: upstream,
		// HTTP/1.1 to the upstream, matching what Envoy's cluster does, so the
		// arms differ only in the client-facing path.
		client: &http.Client{Transport: &http.Transport{
			MaxIdleConns:        512,
			MaxIdleConnsPerHost: 512,
			IdleConnTimeout:     90 * time.Second,
		}},
	}
}

func (f *Frontend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dec, err := FanOut(r.Context(), body, f.hosted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// The shim owns the forward, so it opens the upstream request itself.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, f.upstream+r.URL.Path, bytesReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set(decisionHeader, dec.Endpoint)
	resp, err := f.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// StartFrontend serves cleartext HTTP/2 with prior knowledge, which is the
// common shape for an internal service: plain TCP, no TLS, but h2 framing so
// concurrent requests multiplex over one connection instead of one per request.
func StartFrontend(port int, upstream string, hosted []Binding) (func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return nil, err
	}
	h := NewFrontend(upstream, hosted)
	h2s := &http2.Server{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go h2s.ServeConn(c, &http2.ServeConnOpts{Handler: h})
		}
	}()
	return func() {
		_ = ln.Close()
		<-done
	}, nil
}

// StartUpstream is the model-server stand-in: drain the body, reply small.
func StartUpstream(port int) (func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"bytes":` + itoa(int(n)) + `}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}, nil
}

// newH2CTransport dials cleartext and speaks HTTP/2 with prior knowledge.
func newH2CTransport() *http2.Transport {
	return &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
}
