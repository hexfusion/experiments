package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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

// ServeH2C serves cleartext HTTP/2 so a data plane multiplexes concurrent
// routing calls over one connection rather than opening one per request, which
// matters because this sits on the hot path.
//
// It also serves HTTP/1.1 on the same port. Prior-knowledge h2c alone would be
// faster to write and wrong to operate: kubelet probes, curl and every
// debugging tool speak HTTP/1.1, and a service nobody can probe is a service
// that never goes Ready.
func ServeH2C(ln net.Listener, h http.Handler) error {
	h2s := &http2.Server{
		MaxConcurrentStreams: 1000,
		// The stream window defaults to 64KB, which stalls on the multi-turn
		// bodies this carries. Raise both so a large request does not spend
		// round trips on flow control.
		MaxUploadBufferPerStream:     1 << 20,
		MaxUploadBufferPerConnection: 4 << 20,
	}
	srv := &http.Server{Handler: h2c.NewHandler(h, h2s)}
	return srv.Serve(ln)
}
