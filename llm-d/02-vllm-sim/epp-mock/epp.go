package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/llm-d/llm-d-kv-cache/pkg/kvcache"
	"github.com/llm-d/llm-d-kv-cache/pkg/kvcache/kvblock"
	"github.com/llm-d/llm-d-kv-cache/pkg/kvevents"
	"github.com/llm-d/llm-d-kv-cache/pkg/kvevents/engineadapter"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/hexfusion/experiments/llm-d/internal/extproc"
)

var rrCounter atomic.Uint64

type picker struct {
	extprocv3.UnimplementedExternalProcessorServer
	pods        []string
	indexer     *kvcache.Indexer
	log         *slog.Logger
	streamCount atomic.Uint64
}

// Filter chain order (envoy.yaml): bbr → epp → router. By the time we see
// RequestHeaders, bbr has set x-model-name. We open epp.pick on Headers
// so library spans (llm_d.kv_cache.score_tokens) nest under it; the actual
// pick happens on Body so we can read the prompt and score by prefix.
func (p *picker) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	streamID := p.streamCount.Add(1)
	ctx := extproc.ExtractTraceContextFromGRPCMetadata(stream.Context())
	tracer := otel.Tracer("epp-mock")

	var (
		modelName, tenantID, requestID string
		pickCtx                        context.Context
		pickSpan                       trace.Span
	)

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch r := req.Request.(type) {
		case *extprocv3.ProcessingRequest_RequestHeaders:
			modelName = extproc.HeaderValue(r.RequestHeaders.Headers, "x-model-name")
			tenantID = extproc.HeaderValue(r.RequestHeaders.Headers, "x-tenant-id")
			requestID = extproc.HeaderValue(r.RequestHeaders.Headers, "x-request-id")

			pickCtx, pickSpan = tracer.Start(ctx, "epp.pick",
				trace.WithAttributes(
					attribute.Int64("epp.stream_id", int64(streamID)),
					attribute.String("tenant.id", tenantID),
					attribute.String("request.id", requestID),
					attribute.String("inference.model", modelName),
					attribute.String("picker.strategy", *strategy),
				),
			)

			// CONTINUE; pick after body so we can score by prefix.
			if err := stream.Send(extproc.DefaultContinue(req)); err != nil {
				return err
			}

		case *extprocv3.ProcessingRequest_RequestBody:
			prompt := extractPrompt(r.RequestBody.Body)
			pickSpan.SetAttributes(
				attribute.Int("epp.body_len", len(r.RequestBody.Body)),
				attribute.Int("epp.prompt_len", len(prompt)),
			)

			picked, reason := p.pick(pickCtx, prompt, tenantID, modelName, pickSpan)
			pickedAddr := resolvePod(picked)
			pickSpan.SetAttributes(
				attribute.String("picker.endpoint", picked),
				attribute.String("picker.resolved", pickedAddr),
				attribute.String("picker.reason", reason),
			)
			pickSpan.End()

			p.log.Info("picked",
				"stream_id", streamID,
				"request_id", requestID,
				"tenant_id", tenantID,
				"model", modelName,
				"strategy", *strategy,
				"endpoint", picked,
				"resolved", pickedAddr,
				"reason", reason,
			)

			// HeaderMutation in body response + clear_route_cache: tells
			// Envoy to re-resolve the route so original_dst sees the new
			// x-gateway-destination-endpoint before upstream connect.
			if err := stream.Send(&extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{
					RequestBody: &extprocv3.BodyResponse{
						Response: &extprocv3.CommonResponse{
							Status:          extprocv3.CommonResponse_CONTINUE,
							ClearRouteCache: true,
							HeaderMutation: &extprocv3.HeaderMutation{
								SetHeaders: []*corev3.HeaderValueOption{{
									Header: &corev3.HeaderValue{
										Key:      "x-gateway-destination-endpoint",
										RawValue: []byte(pickedAddr),
									},
								}},
							},
						},
					},
				},
			}); err != nil {
				return err
			}

		// CONTINUE.
		case *extprocv3.ProcessingRequest_ResponseHeaders,
			*extprocv3.ProcessingRequest_ResponseBody,
			*extprocv3.ProcessingRequest_ResponseTrailers,
			*extprocv3.ProcessingRequest_RequestTrailers:
			if resp := extproc.DefaultContinue(req); resp != nil {
				if err := stream.Send(resp); err != nil {
					return err
				}
			}
		}
	}
}

// pick chooses an endpoint. For prefix-aware, it tokenizes with the same
// regex the sim uses and asks the kvcache Indexer which pods hold the most
// matching block keys. Ties and zero-score cases fall back to round-robin.
func (p *picker) pick(ctx context.Context, prompt, tenantID, modelName string, span trace.Span) (endpoint, reason string) {
	if len(p.pods) == 0 {
		return "", "no-pods"
	}
	switch *strategy {
	case "round-robin":
		return p.roundRobin(), "round-robin"
	case "tenant-hash":
		return p.pods[hashIdx(tenantID, len(p.pods))], "tenant-hash"
	case "model-hash":
		return p.pods[hashIdx(modelName, len(p.pods))], "model-hash"
	case "random":
		return p.pods[rand.Intn(len(p.pods))], "random"
	case "prefix-aware":
		return p.pickPrefixAware(ctx, prompt, modelName, span)
	default:
		return p.pods[0], "default-first"
	}
}

func (p *picker) pickPrefixAware(ctx context.Context, prompt, modelName string, span trace.Span) (string, string) {
	if p.indexer == nil || prompt == "" {
		return p.roundRobin(), "rr-fallback-no-indexer-or-prompt"
	}
	tokens := simTokenize(prompt)
	span.SetAttributes(attribute.Int("epp.prompt_tokens", len(tokens)))

	scores, err := p.indexer.ScoreTokens(ctx, tokens, modelName, podIdentifiers(p.pods), nil)
	if err != nil {
		p.log.Warn("kvcache score failed", "err", err)
		return p.roundRobin(), "rr-fallback-score-error"
	}

	bestID, bestScore := "", -1.0
	for podID, s := range scores {
		span.SetAttributes(attribute.Float64("kvcache.score."+podID, s))
		if s > bestScore {
			bestID, bestScore = podID, s
		}
	}
	if bestID == "" || bestScore <= 0 {
		return p.roundRobin(), "rr-fallback-no-prefix-hit"
	}
	if endpoint := matchEndpointToPodID(bestID, p.pods); endpoint != "" {
		return endpoint, fmt.Sprintf("prefix-hit score=%.2f", bestScore)
	}
	return p.roundRobin(), "rr-fallback-podid-unmapped"
}

func (p *picker) roundRobin() string {
	return p.pods[int(rrCounter.Add(1)-1)%len(p.pods)]
}

// startKVCacheIndexer wires up an in-process Indexer + kvevents.Pool +
// per-pod ZMQ subscribers. Hash seed and block size MUST match the sim's
// --hash-seed and --token-block-size or block hashes won't align.
func startKVCacheIndexer(ctx context.Context, log *slog.Logger, pods []string) (*kvcache.Indexer, error) {
	cfg, err := kvcache.NewDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	// We tokenize externally (simTokenize) and call ScoreTokens.
	cfg.TokenizersPoolConfig = nil

	tpCfg := kvblock.DefaultTokenProcessorConfig()
	if seed := os.Getenv("PYTHONHASHSEED"); seed != "" {
		tpCfg.HashSeed = seed
	}
	if bs := os.Getenv("BLOCK_SIZE"); bs != "" {
		var n int
		_, _ = fmt.Sscanf(bs, "%d", &n)
		if n > 0 {
			tpCfg.BlockSize = n
		}
	}
	log.Info("kvcache token-processor", "hash_seed", tpCfg.HashSeed, "block_size", tpCfg.BlockSize)

	tp, err := kvblock.NewChunkedTokenDatabase(tpCfg)
	if err != nil {
		return nil, fmt.Errorf("token database: %w", err)
	}
	indexer, err := kvcache.NewKVCacheIndexer(ctx, cfg, tp)
	if err != nil {
		return nil, fmt.Errorf("indexer: %w", err)
	}
	go indexer.Run(ctx)

	adapter, err := engineadapter.NewAdapter("vllm")
	if err != nil {
		return nil, fmt.Errorf("vllm adapter: %w", err)
	}
	pool := kvevents.NewPool(&kvevents.Config{
		Concurrency: 4,
		TopicFilter: *zmqTopic,
	}, indexer.KVBlockIndex(), tp, adapter)
	pool.Start(ctx)

	// Sim PUBs DIAL the EPP. We BIND one SUB; pod identity comes from
	// the message topic (kv@<POD_IP>@<model>), parsed by the vllm adapter.
	subMgr := kvevents.NewSubscriberManager(pool)
	if err := subMgr.EnsureSubscriber(ctx, "shared-sub", *zmqBind, *zmqTopic, false); err != nil {
		return nil, fmt.Errorf("subscriber bind %s: %w", *zmqBind, err)
	}
	log.Info("kvevents subscriber bound", "endpoint", *zmqBind, "topic", *zmqTopic)
	return indexer, nil
}

// simTokenRE is the byte-for-byte mirror of the sim's SimpleTokenizer
// (github.com/llm-d/llm-d-inference-sim/pkg/tokenizer/tokenizer.go). Must
// stay in sync or block hashes diverge and prefix-aware scoring is zero.
var simTokenRE = regexp.MustCompile(`(\{|\}|:|,|-|\.|\?|\!|;|@|#|\$|%|\^|&|\*|\(|\)|\+|\-|_|~|/|\\|>|<|\[|\]|=|"|\w+)(\s*)`)

func simTokenize(input string) []uint32 {
	strs := simTokenRE.FindAllString(input, -1)
	tokens := make([]uint32, len(strs))
	for i, s := range strs {
		h := fnv.New32a()
		_, _ = h.Write([]byte(s))
		tokens[i] = h.Sum32()
	}
	return tokens
}

// extractPrompt reads the OpenAI completions JSON body and returns the
// prompt. Real EPP also handles chat completions (messages array).
func extractPrompt(body []byte) string {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	if p, ok := doc["prompt"].(string); ok {
		return p
	}
	return ""
}

// resolvePod returns ip:port for Envoy's ORIGINAL_DST cluster. In K8s this
// is a pod IP; in our compose network we resolve the service hostname.
func resolvePod(podHostPort string) string {
	host, port, err := net.SplitHostPort(podHostPort)
	if err != nil {
		return podHostPort
	}
	if ip := net.ParseIP(host); ip != nil {
		return podHostPort
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return podHostPort
	}
	return net.JoinHostPort(addrs[0].IP.String(), port)
}

// podIdentifiers strips :port; sim publishes events keyed by POD_NAME.
func podIdentifiers(pods []string) []string {
	out := make([]string, 0, len(pods))
	for _, p := range pods {
		host, _, err := net.SplitHostPort(p)
		if err != nil {
			out = append(out, p)
			continue
		}
		out = append(out, host)
	}
	return out
}

func matchEndpointToPodID(podID string, pods []string) string {
	for _, p := range pods {
		host, _, err := net.SplitHostPort(p)
		if err == nil && host == podID {
			return p
		}
	}
	return ""
}

func hashIdx(key string, mod int) int {
	if key == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()) % mod
}
