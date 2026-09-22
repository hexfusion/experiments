package main

import (
	"encoding/binary"
	"errors"
	"io"
)

// The session endpoint carries a two-phase exchange over one HTTP/2 stream, so
// each direction needs framing. EPP keeps per-request state on its ext_proc
// stream, so the response phase must reach the same stream that carried the
// request phase; two separate HTTP calls cannot work.
//
// Frames are [type:1][length:4][payload], which is enough structure to
// interleave metadata and raw body bytes without escaping either.
type FrameType uint8

const (
	// Uplink, data plane to gateway.
	FrameRequestHeaders FrameType = 1
	FrameRequestBody    FrameType = 2
	FrameRequestEnd     FrameType = 3
	FrameResponseHeaders FrameType = 4
	FrameResponseBody    FrameType = 5
	FrameResponseEnd     FrameType = 6

	// Downlink, gateway to data plane.
	FrameDecision FrameType = 10
	FrameBody     FrameType = 11
	FrameUsage    FrameType = 12
	FrameError    FrameType = 13
)

const maxFrame = 8 << 20

var errFrameTooLarge = errors.New("frame exceeds maximum size")

func WriteFrame(w io.Writer, t FrameType, payload []byte) error {
	if len(payload) > maxFrame {
		return errFrameTooLarge
	}
	var hdr [5]byte
	hdr[0] = byte(t)
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame returns io.EOF cleanly when the peer half-closes between frames.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, errFrameTooLarge
	}
	if n == 0 {
		return FrameType(hdr[0]), nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return FrameType(hdr[0]), buf, nil
}
