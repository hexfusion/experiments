package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	h1 := flag.Bool("h1", false, "run the HTTP/1.1 control instead of h2c")
	noDuplexUpstream := flag.Bool("no-duplex-upstream", false, "upstream drains the whole request body before responding")
	delayFirstWrite := flag.Duration("delay-first-write", 0, "defer the first upstream body write (exercises golang/go#17480)")
	chunks := flag.Int("chunks", 12, "request body chunks")
	chunkKB := flag.Int("chunk-kb", 32, "size of each request chunk in KB")
	flag.Parse()

	ccfg := DefaultClientConfig()
	ccfg.Chunks = *chunks
	ccfg.ChunkBytes = *chunkKB << 10
	ccfg.H1 = *h1

	ucfg := DefaultUpstreamConfig()
	ucfg.Duplex = !*noDuplexUpstream

	res, hw, tl, err := Run(context.Background(), ccfg, ucfg, *h1, *delayFirstWrite)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	proto := "h2c"
	if *h1 {
		proto = "http/1.1"
	}
	fmt.Printf("protocol            %s\n", proto)
	fmt.Printf("upstream duplex     %v\n", ucfg.Duplex)
	fmt.Printf("request body        %d KB\n", ccfg.BodyBytes()>>10)
	fmt.Printf("response bytes      %d\n", res.ResponseBytes)
	fmt.Printf("policy high water   %d KB (bytes of body held at once)\n", hw>>10)
	fmt.Printf("first response byte %.1fms\n", ms(res.FirstResponseByte))
	fmt.Printf("last request byte   %.1fms\n", ms(res.LastRequestByte))
	fmt.Printf("DUPLEX              %v\n", res.Duplex)
	fmt.Printf("\ntimeline:\n%s", tl)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
