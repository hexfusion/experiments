package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"

	"github.com/hexfusion/experiments/llm-d/internal/extproc"
)

var (
	listenAddr = flag.String("listen", ":9002", "gRPC listen address")
	podsFlag   = flag.String("pods", extproc.EnvOr("POD_ENDPOINTS", ""), "comma-separated host:port list. Env: POD_ENDPOINTS")
	strategy   = flag.String("strategy", extproc.EnvOr("STRATEGY", "round-robin"), "pick strategy: round-robin | tenant-hash | model-hash | random")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(ctx, log); err != nil {
		log.Error("epp failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	pods := extproc.SplitCSV(*podsFlag)
	if len(pods) == 0 {
		return fmt.Errorf("--pods or POD_ENDPOINTS env required")
	}

	shutdown, err := extproc.InitTracer(ctx, "epp-mock")
	if err != nil {
		log.Error("tracing init failed", "err", err)
	} else {
		defer func() { _ = shutdown(ctx) }()
	}

	lis, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen failed addr: %s: %w", *listenAddr, err)
	}

	gs := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(gs, &picker{pods: pods, log: log})

	log.Info("epp-mock startup",
		"listen", *listenAddr,
		"strategy", *strategy,
		"pods", pods,
		"otel_endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if err := gs.Serve(lis); err != nil {
		return fmt.Errorf("serve error: %w", err)
	}
	return nil
}
