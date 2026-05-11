package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/hexfusion/experiments/llm-d/internal/extproc"
)

var (
	listen   = flag.String("listen", ":8000", "HTTP listen address")
	podID    = flag.String("pod-id", extproc.EnvOr("POD_ID", "vllm-mock-unknown"), "this pod's identity (env: POD_ID)")
	delayMs  = flag.Int("delay-ms", 0, "synthetic per-request delay (simulate inference latency)")
	verbose  = flag.Bool("verbose", true, "log every request")
	requests atomic.Uint64
)

// CompletionsRequest is a tiny subset of the OpenAI completions schema.
type CompletionsRequest struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Stream    bool   `json:"stream,omitempty"`
}

type CompletionsResponse struct {
	ID       string   `json:"id"`
	Object   string   `json:"object"`
	Created  int64    `json:"created"`
	Model    string   `json:"model"`
	Choices  []Choice `json:"choices"`
	Usage    Usage    `json:"usage"`
	ServedBy string   `json:"served_by"`
}

type Choice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(ctx, log); err != nil {
		log.Error("vllm-mock failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	shutdown, err := extproc.InitTracer(ctx, "vllm-mock")
	if err != nil {
		log.Error("tracing init failed", "err", err)
	} else {
		defer func() { _ = shutdown(ctx) }()
	}

	s := &server{log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/completions", s.handleCompletions)
	mux.HandleFunc("POST /v1/chat/completions", s.handleCompletions)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// otelhttp middleware extracts W3C trace context from request headers
	// and starts a server span per request.
	handler := otelhttp.NewHandler(mux, "vllm-mock")

	log.Info("vllm-mock startup",
		"listen", *listen,
		"pod_id", *podID,
		"delay_ms", *delayMs,
		"otel_endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

type server struct {
	log *slog.Logger
}

func (s *server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	reqNum := requests.Add(1)
	tenantID := r.Header.Get("X-Tenant-Id")
	requestID := r.Header.Get("X-Request-Id")

	span := trace.SpanFromContext(r.Context())
	span.SetAttributes(
		attribute.String("pod.id", *podID),
		attribute.String("tenant.id", tenantID),
		attribute.String("request.id", requestID),
	)

	var req CompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	span.SetAttributes(
		attribute.String("inference.model", req.Model),
		attribute.Int("inference.prompt_len", len(req.Prompt)),
		attribute.Int("inference.max_tokens", req.MaxTokens),
	)

	if *verbose {
		s.log.Info("completions",
			"req_num", reqNum,
			"pod_id", *podID,
			"tenant_id", tenantID,
			"request_id", requestID,
			"model", req.Model,
			"prompt_len", len(req.Prompt),
			"max_tokens", req.MaxTokens,
			"stream", req.Stream,
		)
	}

	if *delayMs > 0 {
		time.Sleep(time.Duration(*delayMs) * time.Millisecond)
	}

	resp := CompletionsResponse{
		ID:      fmt.Sprintf("cmpl-mock-%d", reqNum),
		Object:  "text_completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{{
			Text:         fmt.Sprintf(" [served by %s] mock completion for prompt of len %d", *podID, len(req.Prompt)),
			Index:        0,
			FinishReason: "stop",
		}},
		Usage: Usage{
			PromptTokens:     len(req.Prompt) / 4,
			CompletionTokens: req.MaxTokens,
			TotalTokens:      len(req.Prompt)/4 + req.MaxTokens,
		},
		ServedBy: *podID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Pod-Id", *podID)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleMetrics emits a tiny synthetic Prometheus set so EPP scoring can
// observe something. Real vLLM emits the full vllm:* family.
func (s *server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	count := requests.Load()
	fmt.Fprintf(w, "# HELP vllm_mock_requests_total Total requests served by this mock pod\n")
	fmt.Fprintf(w, "# TYPE vllm_mock_requests_total counter\n")
	fmt.Fprintf(w, "vllm_mock_requests_total{pod_id=%q} %d\n", *podID, count)
	fmt.Fprintf(w, "vllm:num_requests_running{pod_id=%q} 0\n", *podID)
	fmt.Fprintf(w, "vllm:num_requests_waiting{pod_id=%q} 0\n", *podID)
	fmt.Fprintf(w, "vllm:gpu_cache_usage_perc{pod_id=%q} 0.5\n", *podID)
}
