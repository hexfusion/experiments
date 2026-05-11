package extproc

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"google.golang.org/grpc/metadata"
)

// InitTracer wires up an OTLP/gRPC exporter against OTEL_EXPORTER_OTLP_ENDPOINT.
// Returns a no-op shutdown if the env var is unset (demos still work without
// Jaeger). Caller defers shutdown.
func InitTracer(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
		resource.WithProcessRuntimeDescription(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// headerCarrier adapts a string map to the otel TextMapCarrier interface.
type headerCarrier map[string]string

func (c headerCarrier) Get(key string) string { return c[strings.ToLower(key)] }
func (c headerCarrier) Set(key, value string) { c[strings.ToLower(key)] = value }
func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// ExtractTraceContextFromGRPCMetadata pulls W3C trace context out of the
// gRPC stream metadata. This is where Envoy puts traceparent for ext_proc
// calls — not in the HTTP RequestHeaders payload, since the http filter
// chain runs before traceparent is injected into the upstream request.
func ExtractTraceContextFromGRPCMetadata(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	carrier := headerCarrier{}
	for k, vs := range md {
		if len(vs) > 0 {
			carrier[strings.ToLower(k)] = vs[0]
		}
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
