package extproc

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync/atomic"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// NewBBR returns an ext_proc processor that BUFFERED-reads each request
// body, parses JSON, and sets headerKey to the top-level "model" field.
// Envoy must run this filter with processing_mode.request_body_mode: BUFFERED.
func NewBBR(log *slog.Logger, headerKey string) extprocv3.ExternalProcessorServer {
	return &bbr{log: log, headerKey: headerKey}
}

type bbr struct {
	extprocv3.UnimplementedExternalProcessorServer
	log         *slog.Logger
	headerKey   string
	streamCount atomic.Uint64
}

func (b *bbr) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	streamID := b.streamCount.Add(1)
	ctx := ExtractTraceContextFromGRPCMetadata(stream.Context())
	tracer := otel.Tracer("bbr")

	var requestID, tenantID string

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
			requestID = HeaderValue(r.RequestHeaders.Headers, "x-request-id")
			tenantID = HeaderValue(r.RequestHeaders.Headers, "x-tenant-id")
			// CONTINUE; mutate after we've parsed the body.
			if err := stream.Send(DefaultContinue(req)); err != nil {
				return err
			}

		case *extprocv3.ProcessingRequest_RequestBody:
			body := r.RequestBody.Body
			_, span := tracer.Start(ctx, "bbr.process_body",
				trace.WithAttributes(
					attribute.Int64("bbr.stream_id", int64(streamID)),
					attribute.String("tenant.id", tenantID),
					attribute.String("request.id", requestID),
					attribute.Int("bbr.body_len", len(body)),
				),
			)
			model := extractModel(body)
			span.SetAttributes(attribute.String("bbr.model_extracted", model))
			span.End()

			b.log.Info("model.extracted",
				"stream_id", streamID,
				"request_id", requestID,
				"tenant_id", tenantID,
				"model", model,
				"body_len", len(body),
				"end_of_stream", r.RequestBody.EndOfStream,
			)

			if err := stream.Send(&extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{
					RequestBody: &extprocv3.BodyResponse{
						Response: &extprocv3.CommonResponse{
							Status: extprocv3.CommonResponse_CONTINUE,
							HeaderMutation: &extprocv3.HeaderMutation{
								SetHeaders: []*corev3.HeaderValueOption{{
									Header: &corev3.HeaderValue{
										Key:      b.headerKey,
										RawValue: []byte(model),
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
			if resp := DefaultContinue(req); resp != nil {
				if err := stream.Send(resp); err != nil {
					return err
				}
			}
		}
	}
}

// extractModel reads the JSON body and returns the value of the top-level
// "model" field. Real BBR does this with a streaming JSON tokenizer for
// large bodies.
func extractModel(body []byte) string {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	if m, ok := doc["model"].(string); ok {
		return m
	}
	return ""
}
