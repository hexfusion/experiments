package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// StreamConfig shapes the generated SSE response. IncludeUsage models
// stream_options.include_usage: without it there is no final usage chunk at
// all, so usage-based accounting has nothing to read.
type StreamConfig struct {
	Chunks       int
	ChunkDelay   time.Duration
	TokensPerCh  int
	IncludeUsage bool
}

func DefaultStreamConfig() StreamConfig {
	return StreamConfig{Chunks: 64, ChunkDelay: 0, TokensPerCh: 8, IncludeUsage: true}
}

// StartStreamingUpstream emits an OpenAI-shaped SSE stream: N content chunks,
// then optionally a usage chunk, then [DONE].
func StartStreamingUpstream(port int, cfg StreamConfig) (func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		content := make([]byte, 0, cfg.TokensPerCh*6)
		for i := 0; i < cfg.TokensPerCh; i++ {
			content = append(content, "token "...)
		}
		for i := 0; i < cfg.Chunks; i++ {
			fmt.Fprintf(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"}}]}\n\n", content)
			fl.Flush()
			if cfg.ChunkDelay > 0 {
				time.Sleep(cfg.ChunkDelay)
			}
		}
		if cfg.IncludeUsage {
			fmt.Fprintf(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n",
				1000, cfg.Chunks*cfg.TokensPerCh, 1000+cfg.Chunks*cfg.TokensPerCh)
			fl.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	})}
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }, nil
}

// Usage is the response-side metadata every consumer actually wants.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamDecoder folds SSE chunks into response metadata. It is stateful across
// chunks on purpose: a naive per-chunk split drops any event that straddles a
// chunk boundary, which is the defect the current EPP implementation has.
type StreamDecoder struct {
	buf    bytes.Buffer
	Usage  *Usage
	Events int
	Done   bool
}

// Decode consumes one transport chunk, which may contain part of an event,
// several events, or nothing complete at all.
func (d *StreamDecoder) Decode(chunk []byte) error {
	d.buf.Write(chunk)
	for {
		raw := d.buf.Bytes()
		idx := bytes.Index(raw, []byte("\n\n"))
		if idx < 0 {
			return nil // incomplete event, keep it buffered
		}
		event := make([]byte, idx)
		copy(event, raw[:idx])
		d.buf.Next(idx + 2)

		line := bytes.TrimPrefix(bytes.TrimSpace(event), []byte("data: "))
		if len(line) == 0 {
			continue
		}
		if bytes.Equal(line, []byte("[DONE]")) {
			d.Done = true
			continue
		}
		d.Events++
		var probe struct {
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Usage != nil {
			d.Usage = probe.Usage
		}
	}
}

// ResponseMeta is what the shim publishes to hosted plugins once the response
// completes. Plugins never see response bytes.
type ResponseMeta struct {
	Usage  *Usage
	Events int
}

func (d *StreamDecoder) Meta() ResponseMeta {
	return ResponseMeta{Usage: d.Usage, Events: d.Events}
}

func usageString(u *Usage) string {
	if u == nil {
		return "none"
	}
	return strconv.Itoa(u.TotalTokens)
}
