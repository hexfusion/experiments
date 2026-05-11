package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/llm-d/llm-d-kv-cache/pkg/kvcache"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/hexfusion/experiments/llm-d/internal/extproc"
)

var (
	listenAddr   = flag.String("listen", ":9002", "gRPC listen address")
	podsFlag     = flag.String("pods", extproc.EnvOr("POD_ENDPOINTS", ""), "comma-separated host:port list. Env: POD_ENDPOINTS")
	zmqBind      = flag.String("zmq-bind", extproc.EnvOr("ZMQ_BIND", "tcp://0.0.0.0:5557"), "ZMQ SUB bind address. Sims DIAL this. Env: ZMQ_BIND")
	zmqTopic     = flag.String("zmq-topic", extproc.EnvOr("ZMQ_TOPIC", "kv@"), "ZMQ topic filter. Env: ZMQ_TOPIC")
	strategy     = flag.String("strategy", extproc.EnvOr("STRATEGY", "round-robin"), "pick strategy: round-robin | tenant-hash | model-hash | random | prefix-aware")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(ctx, logger); err != nil {
		logger.Error("epp failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	pods := extproc.SplitCSV(*podsFlag)
	if len(pods) == 0 {
		return fmt.Errorf("--pods or POD_ENDPOINTS env required")
	}
	// kvcache uses controller-runtime's logger; route it to stdout too.
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	shutdown, err := extproc.InitTracer(ctx, "epp-mock")
	if err != nil {
		logger.Error("tracing init failed", "err", err)
	} else {
		defer func() { _ = shutdown(ctx) }()
	}

	var indexer *kvcache.Indexer
	if *strategy == "prefix-aware" {
		idx, err := startKVCacheIndexer(ctx, logger, pods)
		if err != nil {
			return fmt.Errorf("kvcache indexer: %w", err)
		}
		indexer = idx
	}

	lis, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen failed addr: %s: %w", *listenAddr, err)
	}

	gs := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(gs, &picker{
		pods:    pods,
		indexer: indexer,
		log:     logger,
	})

	logger.Info("epp-mock startup",
		"listen", *listenAddr,
		"strategy", *strategy,
		"pods", pods,
		"prefix_aware", indexer != nil,
		"otel_endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if err := gs.Serve(lis); err != nil {
		return fmt.Errorf("serve error: %w", err)
	}
	return nil
}
