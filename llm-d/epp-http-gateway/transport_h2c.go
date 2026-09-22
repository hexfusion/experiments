package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
)

// h2cTransport speaks ext_proc over cleartext HTTP/2 with no gRPC library.
//
// This exists to demonstrate that ext_proc is a data contract rather than a
// protocol. Talking to an unmodified endpoint picker needs only gRPC's wire
// conventions, which are four things: a fixed path, a content type, each message
// framed as a one-byte compression flag and a four-byte big-endian length
// followed by the protobuf, and a status in trailers. Everything else grpc-go
// provides went unused, as the three-method ProcessStream interface shows.
//
// The practical payoff is connection control. grpc-go multiplexes every stream
// over one connection, which silently pinned all traffic to a single picker
// replica until a round-robin service config forced otherwise. Here each stream
// is an ordinary HTTP/2 request and connection behavior is the transport's own.
type h2cTransport struct {
	url    string
	client *http2.Transport
}

const (
	processPath  = "/envoy.service.ext_proc.v3.ExternalProcessor/Process"
	grpcProtoCT  = "application/grpc+proto"
	frameHeadLen = 5
)

func NewH2CTransport(addr string) (Transport, error) {
	url := addr
	if !strings.Contains(url, "://") {
		url = "http://" + url
	}
	return &h2cTransport{
		url: strings.TrimSuffix(url, "/"),
		client: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, a string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, a)
			},
		},
	}, nil
}

func (t *h2cTransport) Close() error {
	t.client.CloseIdleConnections()
	return nil
}

// Ping opens a TCP connection only. It deliberately does not start an exchange,
// so readiness costs the picker nothing.
func (t *h2cTransport) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	host := strings.TrimPrefix(t.url, "http://")
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("picker not reachable: %w", err)
	}
	return c.Close()
}

func (t *h2cTransport) Open(ctx context.Context) (ProcessStream, error) {
	body := newStreamBody()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url+processPath, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", grpcProtoCT)
	req.Header.Set("te", "trailers")

	s := &h2cStream{body: body, respCh: make(chan respOrErr, 1)}
	// RoundTrip returns when the server sends response headers, and a gRPC
	// server sends those when its handler first writes. The handler cannot
	// write until it has received something, so this must not block the first
	// Send. Running it alongside is what makes the exchange bidirectional.
	go func() {
		resp, rerr := t.client.RoundTrip(req)
		s.respCh <- respOrErr{resp: resp, err: rerr}
	}()
	return s, nil
}

type respOrErr struct {
	resp *http.Response
	err  error
}

// h2cStream is one ext_proc exchange over one HTTP/2 stream. Sends write framed
// messages into the request body; receives read them off the response body.
type h2cStream struct {
	body   *streamBody
	respCh chan respOrErr

	once sync.Once
	resp *http.Response
	err  error

	sendMu sync.Mutex
	hdr    [frameHeadLen]byte
}

func (s *h2cStream) Send(m *extProcPb.ProcessingRequest) error {
	size := proto.Size(m)
	buf := getBuf(frameHeadLen + size)
	buf[0] = 0 // not compressed
	binary.BigEndian.PutUint32(buf[1:], uint32(size))
	out, err := proto.MarshalOptions{}.MarshalAppend(buf[:frameHeadLen], m)
	if err != nil {
		putBuf(buf)
		return err
	}
	// One enqueue per message. Framing and payload go together so the
	// transport sees a single chunk, and the queue decouples this call from
	// the transport's read loop. An io.Pipe would block here until the
	// transport read, which serialises concurrent streams against each other.
	return s.body.enqueue(out)
}

func (s *h2cStream) CloseSend() error { s.body.close(); return nil }

// await blocks until response headers arrive, which happens on the first Recv.
func (s *h2cStream) await() error {
	s.once.Do(func() {
		r := <-s.respCh
		s.resp, s.err = r.resp, r.err
		if s.err == nil && s.resp.StatusCode != http.StatusOK {
			s.err = fmt.Errorf("picker returned HTTP %d", s.resp.StatusCode)
		}
		if s.err == nil {
			if st := s.resp.Header.Get("grpc-status"); st != "" && st != "0" {
				s.err = fmt.Errorf("grpc-status %s: %s", st, s.resp.Header.Get("grpc-message"))
			}
		}
	})
	return s.err
}

func (s *h2cStream) Recv() (*extProcPb.ProcessingResponse, error) {
	if err := s.await(); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(s.resp.Body, s.hdr[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			// Clean end of stream. A non-zero status in trailers is the
			// picker reporting an error rather than simply finishing.
			if st := s.resp.Trailer.Get("grpc-status"); st != "" && st != "0" {
				return nil, fmt.Errorf("grpc-status %s: %s", st, s.resp.Trailer.Get("grpc-message"))
			}
			return nil, io.EOF
		}
		return nil, err
	}
	n := binary.BigEndian.Uint32(s.hdr[1:])
	if n > 64<<20 {
		return nil, fmt.Errorf("message of %d bytes exceeds limit", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.resp.Body, buf); err != nil {
		return nil, err
	}
	out := &extProcPb.ProcessingResponse{}
	if err := proto.Unmarshal(buf, out); err != nil {
		return nil, err
	}
	return out, nil
}


// streamBody is the request body for one exchange. It is a queue rather than an
// io.Pipe because a pipe is unbuffered: every write blocks until the transport
// reads, which under concurrency serialises streams against one another. That
// showed up as a large parallel-throughput gap against grpc-go, which buffers.
type streamBody struct {
	ch     chan []byte
	cur    []byte
	closeC sync.Once
}

func newStreamBody() *streamBody {
	return &streamBody{ch: make(chan []byte, 16)}
}

func (b *streamBody) enqueue(frame []byte) error {
	defer func() {
		// Sending on a closed channel panics; a closed stream is not an error
		// worth crashing over.
		_ = recover()
	}()
	b.ch <- frame
	return nil
}

func (b *streamBody) close() {
	b.closeC.Do(func() { close(b.ch) })
}

func (b *streamBody) Read(p []byte) (int, error) {
	for len(b.cur) == 0 {
		chunk, ok := <-b.ch
		if !ok {
			return 0, io.EOF
		}
		b.cur = chunk
	}
	n := copy(p, b.cur)
	b.cur = b.cur[n:]
	if len(b.cur) == 0 {
		putBuf(b.cur[:0])
	}
	return n, nil
}

// Recv allocates a buffer per message; pooling them keeps large multi-turn
// bodies from dominating allocation.
var bufPool = sync.Pool{New: func() any { s := make([]byte, 0, 64<<10); return &s }}

func getBuf(n int) []byte {
	p := bufPool.Get().(*[]byte)
	if cap(*p) < n {
		bufPool.Put(p)
		return make([]byte, n)
	}
	return (*p)[:n]
}

func putBuf(b []byte) {
	if cap(b) == 0 || cap(b) > 1<<20 {
		return
	}
	s := b[:0]
	bufPool.Put(&s)
}
