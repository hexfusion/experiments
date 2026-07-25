package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// phaseEPP records what reached each phase, so a test can assert the response
// phase landed on the same stream as the request phase.
type phaseEPP struct {
	fakeEPP
	streams        int
	respHeaders    int
	respBodyChunks int
	sameStream     bool
}

func (f *phaseEPP) Process(stream extProcPb.ExternalProcessor_ProcessServer) error {
	f.streams++
	var buf []byte
	sawRequest := false
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			if err := stream.Send(headersAck()); err != nil {
				return err
			}
		case *extProcPb.ProcessingRequest_RequestBody:
			buf = append(buf, v.RequestBody.GetBody()...)
			if !v.RequestBody.GetEndOfStream() {
				continue
			}
			f.sawBody = append([]byte(nil), buf...)
			sawRequest = true
			if err := stream.Send(routeDecision(f.destination, buf)); err != nil {
				return err
			}
		case *extProcPb.ProcessingRequest_ResponseHeaders:
			f.respHeaders++
			// The response phase arrived on the stream that carried the
			// request, which is what EPP requires to attach usage.
			f.sameStream = sawRequest
			if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_ResponseHeaders{
					ResponseHeaders: &extProcPb.HeadersResponse{Response: &extProcPb.CommonResponse{}},
				},
			}); err != nil {
				return err
			}
		case *extProcPb.ProcessingRequest_ResponseBody:
			f.respBodyChunks++
			if err := stream.Send(&extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_ResponseBody{
					ResponseBody: &extProcPb.BodyResponse{Response: &extProcPb.CommonResponse{}},
				},
			}); err != nil {
				return err
			}
		}
	}
}

// session drives the duplex endpoint the way a data plane would, and returns
// the frames it received.
func session(t *testing.T, base string, body string, respChunks []string, abort bool) []frame {
	t.Helper()
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/session", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/octet-stream")

	type result struct {
		resp *http.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := h2cClient().Do(req)
		ch <- result{resp, err}
	}()

	hdrs, _ := json.Marshal(map[string]string{":path": "/v1/chat/completions"})
	if err := WriteFrame(pw, FrameRequestHeaders, hdrs); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(pw, FrameRequestBody, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(pw, FrameRequestEnd, nil); err != nil {
		t.Fatal(err)
	}

	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer r.resp.Body.Close()

	var frames []frame
	// The decision and the forwarded body come back before the caller has even
	// contacted the upstream. That is the duplex property this depends on.
	for i := 0; i < 2; i++ {
		ft, payload, err := ReadFrame(r.resp.Body)
		if err != nil {
			t.Fatalf("reading decision frames: %v", err)
		}
		frames = append(frames, frame{ft, payload})
		if ft == FrameDecision {
			var probe map[string]any
			_ = json.Unmarshal(payload, &probe)
			if _, shed := probe["immediate"]; shed {
				_ = pw.Close()
				return frames
			}
		}
	}

	rh, _ := json.Marshal(map[string]any{
		"status":  "200",
		"headers": map[string]string{"content-type": "text/event-stream"},
	})
	if err := WriteFrame(pw, FrameResponseHeaders, rh); err != nil {
		t.Fatal(err)
	}
	for _, c := range respChunks {
		if err := WriteFrame(pw, FrameResponseBody, []byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if abort {
		// Client disconnected mid-stream.
		_ = pw.Close()
	} else {
		if err := WriteFrame(pw, FrameResponseEnd, nil); err != nil {
			t.Fatal(err)
		}
		_ = pw.Close()
	}

	for {
		ft, payload, err := ReadFrame(r.resp.Body)
		if err != nil {
			break
		}
		frames = append(frames, frame{ft, payload})
		if ft == FrameUsage || ft == FrameError {
			break
		}
	}
	return frames
}

type frame struct {
	t       FrameType
	payload []byte
}

func findFrame(frames []frame, t FrameType) ([]byte, bool) {
	for _, f := range frames {
		if f.t == t {
			return f.payload, true
		}
	}
	return nil, false
}

func headersAck() *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extProcPb.HeadersResponse{Response: &extProcPb.CommonResponse{}},
		},
	}
}

// TestSessionCarriesBothPhasesOnOneStream is the constraint that forces this
// design: EPP keeps per-request state on its ext_proc stream, so usage reported
// on a second stream would have nothing to attach to.
func TestSessionCarriesBothPhasesOnOneStream(t *testing.T) {
	f := &phaseEPP{fakeEPP: fakeEPP{destination: "10.0.0.9:8000"}}
	base, g := startGateway(t, startFakeEPP(t, f))

	chunks := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n",
		"data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n",
		"data: [DONE]\n\n",
	}
	frames := session(t, base, chatBody, chunks, false)

	raw, ok := findFrame(frames, FrameDecision)
	if !ok {
		t.Fatal("no decision frame")
	}
	var dec Decision
	if err := json.Unmarshal(raw, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Destination != "10.0.0.9:8000" {
		t.Fatalf("destination %q", dec.Destination)
	}

	usageRaw, ok := findFrame(frames, FrameUsage)
	if !ok {
		t.Fatal("no usage frame")
	}
	var u Usage
	if err := json.Unmarshal(usageRaw, &u); err != nil {
		t.Fatal(err)
	}
	if u.TotalTokens != 15 || u.PromptTokens != 10 || u.CompletionTokens != 5 {
		t.Fatalf("usage %+v", u)
	}
	if !u.Complete {
		t.Fatal("usage should be marked complete")
	}

	if f.streams != 1 {
		t.Fatalf("EPP saw %d streams, want 1", f.streams)
	}
	if !f.sameStream {
		t.Fatal("response phase did not reach the stream that carried the request")
	}
	if f.respHeaders != 1 {
		t.Fatalf("EPP saw %d response-header messages", f.respHeaders)
	}
	if reported, aborted := g.ResponseStats(); reported != 1 || aborted != 0 {
		t.Fatalf("reported=%d aborted=%d", reported, aborted)
	}
}

// TestUsageStraddlingChunkBoundary is the defect this decoder exists to avoid:
// splitting per transport chunk drops an event spanning a boundary.
func TestUsageStraddlingChunkBoundary(t *testing.T) {
	f := &phaseEPP{fakeEPP: fakeEPP{destination: "10.0.0.1:8000"}}
	base, _ := startGateway(t, startFakeEPP(t, f))

	full := "data: {\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n"
	split := len(full) / 2
	frames := session(t, base, chatBody, []string{full[:split], full[split:]}, false)

	usageRaw, ok := findFrame(frames, FrameUsage)
	if !ok {
		t.Fatal("no usage frame")
	}
	var u Usage
	if err := json.Unmarshal(usageRaw, &u); err != nil {
		t.Fatal(err)
	}
	if u.TotalTokens != 10 {
		t.Fatalf("usage lost across the chunk boundary: %+v", u)
	}
}

// TestAbortedStreamIsMarkedIncomplete keeps a partial count from reading as a
// real one, which is how a client timeout becomes a silent token undercount.
func TestAbortedStreamIsMarkedIncomplete(t *testing.T) {
	f := &phaseEPP{fakeEPP: fakeEPP{destination: "10.0.0.4:8000"}}
	base, g := startGateway(t, startFakeEPP(t, f))

	frames := session(t, base, chatBody, []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n",
	}, true)

	usageRaw, ok := findFrame(frames, FrameUsage)
	if !ok {
		t.Fatal("no usage frame on abort")
	}
	var u Usage
	if err := json.Unmarshal(usageRaw, &u); err != nil {
		t.Fatal(err)
	}
	if u.Complete {
		t.Fatal("an aborted stream must not be reported complete")
	}
	if u.Events != 1 {
		t.Fatalf("events %d, want 1", u.Events)
	}
	if reported, aborted := g.ResponseStats(); reported != 0 || aborted != 1 {
		t.Fatalf("reported=%d aborted=%d", reported, aborted)
	}
}

// TestDecisionArrivesBeforeResponsePhase is the duplex property: the caller
// gets its destination and can forward while the stream stays open.
func TestDecisionArrivesBeforeResponsePhase(t *testing.T) {
	f := &phaseEPP{fakeEPP: fakeEPP{destination: "10.0.0.5:8000"}}
	base, _ := startGateway(t, startFakeEPP(t, f))

	pr, pw := io.Pipe()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/session", pr)
	type result struct {
		resp *http.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := h2cClient().Do(req)
		ch <- result{resp, err}
	}()

	hdrs, _ := json.Marshal(map[string]string{":path": "/v1/chat/completions"})
	_ = WriteFrame(pw, FrameRequestHeaders, hdrs)
	_ = WriteFrame(pw, FrameRequestBody, []byte(chatBody))
	_ = WriteFrame(pw, FrameRequestEnd, nil)

	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer r.resp.Body.Close()

	start := time.Now()
	ft, payload, err := ReadFrame(r.resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if ft != FrameDecision {
		t.Fatalf("first frame is %d, want a decision", ft)
	}
	var dec Decision
	_ = json.Unmarshal(payload, &dec)
	if dec.Destination != "10.0.0.5:8000" {
		t.Fatalf("destination %q", dec.Destination)
	}
	// The request half-closes only after this point, so the decision demonstrably
	// arrived while the stream was still open for writing.
	if err := WriteFrame(pw, FrameResponseEnd, nil); err != nil {
		t.Fatalf("stream was not still writable after the decision: %v", err)
	}
	_ = pw.Close()
	t.Logf("decision returned in %v with the uplink still open", elapsed)
}

var _ = bytes.Repeat
