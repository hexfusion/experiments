package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9002", "ext_proc listen address")
	cfgPath := flag.String("plugins", "", "path to a JSON array of plugin specs")
	flag.Parse()

	var plugins []PluginSpec
	if *cfgPath != "" {
		raw, err := os.ReadFile(*cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read plugins:", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(raw, &plugins); err != nil {
			fmt.Fprintln(os.Stderr, "parse plugins:", err)
			os.Exit(1)
		}
	}

	b := NewBridge(plugins, OpenAIExtractor{})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	s := grpc.NewServer(grpc.MaxRecvMsgSize(64<<20), grpc.MaxSendMsgSize(64<<20))
	extProcPb.RegisterExternalProcessorServer(s, b)

	fmt.Printf("ext_proc on %s, %d plugin(s), body returned to gateway: %v\n",
		*addr, len(plugins), b.returnBody)
	for _, p := range plugins {
		fmt.Printf("  %-16s %s  needs_body=%v mutates=%v wants_response=%v\n",
			p.Name, p.URL, p.NeedsBody, p.MutatesRequest, p.WantsResponse)
	}
	if err := s.Serve(ln); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}
