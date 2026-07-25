package main

import (
	"bytes"
	"context"
	"errors"
	"io"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// EPPSession holds one ext_proc stream across both phases, which is what EPP
// requires: its per-request context lives on the stream, so usage reported on a
// second stream would have nothing to attach to.
type EPPSession struct {
	stream ProcessStream
	closed bool
}

func (c *EPPClient) NewSession(ctx context.Context) (*EPPSession, error) {
	stream, err := c.transport.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &EPPSession{stream: stream}, nil
}

// RequestPhase sends headers and body and reads until EPP has produced a
// routing decision. The stream stays open for the response phase.
func (s *EPPSession) RequestPhase(headers map[string]string, body []byte) (*RouteResult, error) {
	hm := &corev3.HeaderMap{}
	for k, v := range headers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, RawValue: []byte(v)})
	}
	if err := s.stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{Headers: hm},
		},
	}); err != nil {
		return nil, err
	}
	if err := sendBodyChunks(s.stream, body); err != nil {
		return nil, err
	}

	res := &RouteResult{SetHeaders: map[string]string{}}
	var rebuilt bytes.Buffer
	c := &EPPClient{}
	for {
		resp, err := s.stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		done, aerr := c.absorb(resp, res, &rebuilt)
		if aerr != nil {
			return nil, aerr
		}
		if done {
			break
		}
	}
	if rebuilt.Len() > 0 {
		res.Body = rebuilt.Bytes()
		res.BodyModified = !bytes.Equal(res.Body, body)
	}
	if d, ok := res.SetHeaders[destinationHeader]; ok {
		res.Destination = d
	}
	return res, nil
}

// ResponseHeaders reports the upstream response status and headers. EPP uses
// these to decide whether the response is streamed, which changes how it reads
// the body.
func (s *EPPSession) ResponseHeaders(status string, headers map[string]string) error {
	hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
		{Key: ":status", RawValue: []byte(status)},
	}}
	for k, v := range headers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, RawValue: []byte(v)})
	}
	if err := s.stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extProcPb.HttpHeaders{Headers: hm},
		},
	}); err != nil {
		return err
	}
	return s.drainOne()
}

// ResponseBody streams one chunk of the upstream response to EPP. The caller
// relays chunks as they arrive, so usage extraction sees the same event
// boundaries the client does.
func (s *EPPSession) ResponseBody(chunk []byte, endOfStream bool) error {
	if err := s.stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_ResponseBody{
			ResponseBody: &extProcPb.HttpBody{Body: chunk, EndOfStream: endOfStream},
		},
	}); err != nil {
		return err
	}
	return s.drainOne()
}

// drainOne consumes the acknowledgement EPP sends per response-phase message.
// Not reading these stalls the stream once flow control fills.
func (s *EPPSession) drainOne() error {
	_, err := s.stream.Recv()
	if errors.Is(err, io.EOF) {
		s.closed = true
		return nil
	}
	return err
}

// Close half-closes toward EPP so it can finalize the request.
func (s *EPPSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.CloseSend()
}

func sendBodyChunks(stream ProcessStream, body []byte) error {
	const limit = 62000
	if len(body) == 0 {
		return stream.Send(&extProcPb.ProcessingRequest{
			Request: &extProcPb.ProcessingRequest_RequestBody{
				RequestBody: &extProcPb.HttpBody{EndOfStream: true},
			},
		})
	}
	for start := 0; start < len(body); start += limit {
		end := min(start+limit, len(body))
		if err := stream.Send(&extProcPb.ProcessingRequest{
			Request: &extProcPb.ProcessingRequest_RequestBody{
				RequestBody: &extProcPb.HttpBody{
					Body:        body[start:end],
					EndOfStream: end >= len(body),
				},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
