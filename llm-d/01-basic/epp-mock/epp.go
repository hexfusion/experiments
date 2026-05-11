package main

import (
	"context"
	"hash/fnv"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"sync/atomic"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/hexfusion/experiments/llm-d/internal/extproc"
)

var rrCounter atomic.Uint64

type picker struct {
	extprocv3.UnimplementedExternalProcessorServer
	pods        []string
	log         *slog.Logger
	streamCount atomic.Uint64
}

// 01-basic picks on RequestHeaders alone — no body inspection. Real EPP
// also reads the body for token counts and prompt-aware scoring; that
// happens in 02-vllm-sim.
func (p *picker) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	streamID := p.streamCount.Add(1)
	ctx := extproc.ExtractTraceContextFromGRPCMetadata(stream.Context())
	tracer := otel.Tracer("epp-mock")

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
			modelName := extproc.HeaderValue(r.RequestHeaders.Headers, "x-model-name")
			tenantID := extproc.HeaderValue(r.RequestHeaders.Headers, "x-tenant-id")
			requestID := extproc.HeaderValue(r.RequestHeaders.Headers, "x-request-id")

			_, span := tracer.Start(ctx, "epp.pick",
				trace.WithAttributes(
					attribute.Int64("epp.stream_id", int64(streamID)),
					attribute.String("tenant.id", tenantID),
					attribute.String("request.id", requestID),
					attribute.String("inference.model", modelName),
					attribute.String("picker.strategy", *strategy),
				),
			)

			picked := p.pick(tenantID, modelName)
			pickedAddr := resolvePod(picked)
			span.SetAttributes(
				attribute.String("picker.endpoint", picked),
				attribute.String("picker.resolved", pickedAddr),
			)
			span.End()

			p.log.Info("picked",
				"stream_id", streamID,
				"request_id", requestID,
				"tenant_id", tenantID,
				"model", modelName,
				"strategy", *strategy,
				"endpoint", picked,
				"resolved", pickedAddr,
			)

			if err := stream.Send(&extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							Status: extprocv3.CommonResponse_CONTINUE,
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
		case *extprocv3.ProcessingRequest_RequestBody,
			*extprocv3.ProcessingRequest_ResponseHeaders,
			*extprocv3.ProcessingRequest_ResponseBody,
			*extprocv3.ProcessingRequest_RequestTrailers,
			*extprocv3.ProcessingRequest_ResponseTrailers:
			if resp := extproc.DefaultContinue(req); resp != nil {
				if err := stream.Send(resp); err != nil {
					return err
				}
			}
		}
	}
}

func (p *picker) pick(tenantID, modelName string) string {
	if len(p.pods) == 0 {
		return ""
	}
	switch *strategy {
	case "round-robin":
		return p.pods[int(rrCounter.Add(1)-1)%len(p.pods)]
	case "tenant-hash":
		return p.pods[hashIdx(tenantID, len(p.pods))]
	case "model-hash":
		return p.pods[hashIdx(modelName, len(p.pods))]
	case "random":
		return p.pods[rand.Intn(len(p.pods))]
	default:
		return p.pods[0]
	}
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

func hashIdx(key string, mod int) int {
	if key == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()) % mod
}
