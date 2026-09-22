package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// UpstreamConfig models a server that begins emitting a response before the
// request body has finished arriving. This is the shape a realtime API or a
// streaming-prefill engine has, and the case plain request/response cannot carry.
type UpstreamConfig struct {
	Tokens        int
	TokenInterval time.Duration
	ReadChunk     int
	// Duplex false makes the upstream drain the whole body before responding,
	// which is the non-duplex control for the comparison.
	Duplex bool
}

func DefaultUpstreamConfig() UpstreamConfig {
	return UpstreamConfig{Tokens: 8, TokenInterval: 25 * time.Millisecond, ReadChunk: 4096, Duplex: true}
}

// UpstreamHandler returns the model-server stand-in. It reports observed bytes
// and timings on tl.
func UpstreamHandler(cfg UpstreamConfig, tl *Timeline) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		tl.Mark("upstream.headers-received")

		buf := make([]byte, cfg.ReadChunk)
		n, err := r.Body.Read(buf)
		if n > 0 {
			tl.Mark("upstream.first-body-chunk-read")
		}
		if err != nil && err != io.EOF {
			http.Error(w, "read: "+err.Error(), http.StatusBadRequest)
			return
		}

		read := int64(n)
		drained := make(chan int64, 1)
		go func() {
			extra, _ := io.Copy(io.Discard, r.Body)
			tl.Set("upstream.request-body-drained")
			drained <- extra
		}()

		if !cfg.Duplex {
			read += <-drained
		}

		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("x-upstream-duplex", fmt.Sprint(cfg.Duplex))
		w.WriteHeader(http.StatusOK)

		for i := 0; i < cfg.Tokens; i++ {
			if _, err := fmt.Fprintf(w, "data: {\"token\":%d}\n\n", i); err != nil {
				log.Printf("upstream: write token %d: %v", i, err)
				return
			}
			flusher.Flush()
			if i == 0 {
				tl.Mark("upstream.first-token-written")
			}
			time.Sleep(cfg.TokenInterval)
		}

		if cfg.Duplex {
			read += <-drained
		}
		fmt.Fprintf(w, "data: {\"done\":true,\"request_bytes\":%d}\n\n", read)
		flusher.Flush()
		tl.Set("upstream.response-complete")
	})
}
