package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"golang.org/x/net/http2"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9100", "HTTP listen address")
	eppAddr := flag.String("epp", "127.0.0.1:9002", "EPP ext_proc gRPC address")
	flag.Parse()

	epp, err := NewEPPClient(*eppAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "epp client:", err)
		os.Exit(1)
	}
	defer epp.Close()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	fmt.Printf("routing gateway on %s (h2c), EPP at %s\n", *addr, *eppAddr)
	if err := ServeH2C(ln, NewGateway(epp).Handler()); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}

// ServeH2C serves cleartext HTTP/2 with prior knowledge, so a data plane
// multiplexes concurrent routing calls over one connection rather than opening
// one per request. That matters here because this sits on the hot path.
func ServeH2C(ln net.Listener, h http.Handler) error {
	h2s := &http2.Server{
		MaxConcurrentStreams: 1000,
		// The stream window defaults to 64KB, which stalls on the multi-turn
		// bodies this carries. Raise both so a large request does not spend
		// round trips on flow control.
		MaxUploadBufferPerStream:     1 << 20,
		MaxUploadBufferPerConnection: 4 << 20,
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go h2s.ServeConn(c, &http2.ServeConnOpts{Handler: h})
	}
}
