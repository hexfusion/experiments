package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Usage is what the response phase produces. Complete distinguishes a real zero
// from a stream that never finished, so a client disconnect does not silently
// become a token undercount.
type Usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	Events           int  `json:"events"`
	Complete         bool `json:"complete"`
}

// Session is the duplex endpoint: one HTTP/2 stream carrying both phases.
//
// EPP keeps per-request state on its ext_proc stream, so the response phase has
// to reach the same stream that carried the request. That rules out reporting
// the response with a second HTTP call, and it is why this is full duplex
// rather than two round trips.
//
// Two logical responses travel down the stream. The first is the routing
// decision, sent as soon as EPP produces it so the caller can forward while the
// stream stays open. The second is the usage report, sent once the caller has
// relayed the upstream response.
func (g *Gateway) Session(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if r.ProtoMajor < 2 {
		http.Error(w, "session requires HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}

	sess, err := g.epp.NewSession(r.Context())
	if err != nil {
		g.failures.Add(1)
		http.Error(w, "epp session: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = sess.Close() }()

	w.Header().Set("content-type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if err := g.runSession(r.Body, w, flusher, sess); err != nil {
		g.failures.Add(1)
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		_ = WriteFrame(w, FrameError, payload)
		flusher.Flush()
	}
}

func (g *Gateway) runSession(in io.Reader, out io.Writer, flusher http.Flusher, sess *EPPSession) error {
	var (
		headers    map[string]string
		body       []byte
		decided    bool
		respStatus = "200"
		usage      Usage
		decoder    = &SSEDecoder{}
	)

	for {
		t, payload, err := ReadFrame(in)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		switch t {
		case FrameRequestHeaders:
			if err := json.Unmarshal(payload, &headers); err != nil {
				return err
			}

		case FrameRequestBody:
			body = append(body, payload...)

		case FrameRequestEnd:
			// Phase one. The decision goes back immediately so the caller can
			// forward upstream while this stream stays open for phase two.
			res, err := sess.RequestPhase(normalizeHeaders(headers, len(body)), body)
			if err != nil {
				return err
			}
			if res.Immediate != nil {
				g.shed.Add(1)
				payload, _ := json.Marshal(map[string]any{
					"immediate": map[string]any{
						"status": res.Immediate.Status,
						"body":   res.Immediate.Body,
					},
				})
				if err := WriteFrame(out, FrameDecision, payload); err != nil {
					return err
				}
				flusher.Flush()
				return nil
			}
			dec := Decision{
				Destination:   res.Destination,
				SetHeaders:    res.SetHeaders,
				RemoveHeaders: res.RemoveHeaders,
				BodyModified:  res.BodyModified,
			}
			raw, err := json.Marshal(dec)
			if err != nil {
				return err
			}
			if err := WriteFrame(out, FrameDecision, raw); err != nil {
				return err
			}
			forward := body
			if res.Body != nil {
				forward = res.Body
			}
			if res.BodyModified {
				g.rewrote.Add(1)
			}
			if err := WriteFrame(out, FrameBody, forward); err != nil {
				return err
			}
			flusher.Flush()
			decided = true
			g.routed.Add(1)

		case FrameResponseHeaders:
			if !decided {
				return errors.New("response headers before a routing decision")
			}
			var rh struct {
				Status  string            `json:"status"`
				Headers map[string]string `json:"headers"`
			}
			if err := json.Unmarshal(payload, &rh); err != nil {
				return err
			}
			if rh.Status != "" {
				respStatus = rh.Status
			}
			if err := sess.ResponseHeaders(respStatus, rh.Headers); err != nil {
				return err
			}

		case FrameResponseBody:
			// Relayed as it arrives, so EPP sees the event boundaries the
			// client sees rather than a reassembled blob.
			if err := decoder.Decode(payload); err != nil {
				return err
			}
			if err := sess.ResponseBody(payload, false); err != nil {
				return err
			}

		case FrameResponseEnd:
			if err := sess.ResponseBody(nil, true); err != nil {
				return err
			}
			usage = decoder.Usage()
			usage.Complete = true
			raw, err := json.Marshal(usage)
			if err != nil {
				return err
			}
			if err := WriteFrame(out, FrameUsage, raw); err != nil {
				return err
			}
			flusher.Flush()
			g.reported.Add(1)
			return nil
		}
	}

	// The caller went away mid-stream. Report what was seen and mark it
	// incomplete rather than letting a partial count read as a real one.
	if decided {
		usage = decoder.Usage()
		usage.Complete = false
		raw, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		if err := WriteFrame(out, FrameUsage, raw); err != nil {
			return err
		}
		flusher.Flush()
		g.aborted.Add(1)
	}
	return nil
}

func normalizeHeaders(h map[string]string, bodyLen int) map[string]string {
	out := map[string]string{
		":path":          "/v1/chat/completions",
		":method":        "POST",
		"content-type":   "application/json",
		"content-length": itoa(bodyLen),
	}
	for k, v := range h {
		out[k] = v
	}
	return out
}
