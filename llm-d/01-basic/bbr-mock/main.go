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
	listenAddr = flag.String("listen", ":9001", "gRPC listen address")
	headerKey  = flag.String("header", "x-model-name", "header to set with extracted model name")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(ctx, log); err != nil {
		log.Error("bbr failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	shutdown, err := extproc.InitTracer(ctx, "bbr-mock")
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
	extprocv3.RegisterExternalProcessorServer(gs, extproc.NewBBR(log, *headerKey))

	log.Info("bbr-mock startup",
		"listen", *listenAddr,
		"header_key", *headerKey,
		"otel_endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if err := gs.Serve(lis); err != nil {
		return fmt.Errorf("serve error: %w", err)
	}
	return nil
}
