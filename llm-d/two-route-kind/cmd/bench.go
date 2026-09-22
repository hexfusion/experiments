package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// runBench times the two ways of asking the same EPP for the same decision.
//
// This isolates the transport and nothing else: one EPP, one scheduler, one
// body. It deliberately does not measure the two-route topology, which adds a
// gateway traversal and a process and is a separate number.
func runBench() {
	var (
		grpcAddr = env("EPP_GRPC", "")
		httpAddr = env("EPP_HTTP", "")
		path     = env("PATH_", "/v1/chat/completions")
		n        = atoiOr(env("REQUESTS", "2000"), 2000)
		conc     = atoiOr(env("CONCURRENCY", "50"), 50)
		bodyKB   = atoiOr(env("BODY_KB", "0"), 0)
	)
	if grpcAddr == "" || httpAddr == "" {
		log.Fatal("set EPP_GRPC and EPP_HTTP")
	}
	body := chatBody(bodyKB)
	fmt.Printf("body %d bytes, %d requests, concurrency %d\n\n", len(body), n, conc)

	// ARM runs a single transport so an external sampler can attribute CPU and
	// memory to it. Unset runs both, which is right for latency but useless for
	// resource attribution since the arms overlap in the sampling window.
	switch env("ARM", "") {
	case "http":
		best(benchHTTP(httpAddr, path, body, n, conc), benchHTTP(httpAddr, path, body, n, conc)).print("http     ")
		return
	case "ext_proc":
		best(benchExtProc(grpcAddr, path, body, n, conc), benchExtProc(grpcAddr, path, body, n, conc)).print("ext_proc ")
		return
	}

	// HTTP first so the gRPC arm cannot claim a cold-start advantage, then both
	// again, and the better run of each is reported. Ordering effects on a busy
	// laptop are larger than the difference being measured.
	hb := benchHTTP(httpAddr, path, body, n, conc)
	gb := benchExtProc(grpcAddr, path, body, n, conc)
	hb2 := benchHTTP(httpAddr, path, body, n, conc)
	gb2 := benchExtProc(grpcAddr, path, body, n, conc)

	h := best(hb, hb2)
	g := best(gb, gb2)
	h.print("http     ")
	g.print("ext_proc ")
	if h.p50 > 0 && g.p50 > 0 {
		fmt.Printf("\nhttp vs ext_proc: p50 %.2fx, p99 %.2fx, throughput %.2fx\n",
			float64(h.p50)/float64(g.p50), float64(h.p99)/float64(g.p99), h.rps/g.rps)
	}
}

type bench struct {
	p50, p99 time.Duration
	rps      float64
	errs     int
	decided  int
}

func best(a, b bench) bench {
	if b.errs < a.errs || (b.errs == a.errs && b.p50 < a.p50) {
		return b
	}
	return a
}

func (b bench) print(name string) {
	fmt.Printf("%s p50 %-10v p99 %-10v %8.0f req/s  decided %d  errors %d\n",
		name, b.p50, b.p99, b.rps, b.decided, b.errs)
}

func summarise(durs []time.Duration, elapsed time.Duration, n, errs, decided int) bench {
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	b := bench{rps: float64(n) / elapsed.Seconds(), errs: errs, decided: decided}
	if len(durs) > 0 {
		b.p50 = durs[len(durs)/2]
		b.p99 = durs[len(durs)*99/100]
	}
	return b
}

func benchHTTP(addr, path string, body []byte, n, conc int) bench {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{MaxIdleConns: conc * 2, MaxIdleConnsPerHost: conc * 2},
	}
	durs := make([]time.Duration, n)
	ok := make([]bool, n)
	fail := make([]bool, n)

	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			t0 := time.Now()
			req, _ := http.NewRequest("POST", addr+path, bytesReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				fail[i] = true
				durs[i] = time.Since(t0)
				return
			}
			var d decision
			_ = json.NewDecoder(resp.Body).Decode(&d)
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			durs[i] = time.Since(t0)
			ok[i] = d.Endpoint != ""
		}(i)
	}
	wg.Wait()
	return tally(durs, time.Since(start), n, ok, fail)
}

var firstErr sync.Once

func benchExtProc(addr, path string, body []byte, n, conc int) bench {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("ext_proc dial: %v", err)
	}
	defer conn.Close()
	client := extProcPb.NewExternalProcessorClient(conn)

	durs := make([]time.Duration, n)
	ok := make([]bool, n)
	fail := make([]bool, n)

	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			t0 := time.Now()
			dest, err := extProcDecide(client, path, body)
			durs[i] = time.Since(t0)
			if err != nil {
				fail[i] = true
				firstErr.Do(func() { log.Printf("ext_proc error: %v", err) })
				return
			}
			ok[i] = dest != ""
		}(i)
	}
	wg.Wait()
	return tally(durs, time.Since(start), n, ok, fail)
}

// extProcDecide runs one request phase over a fresh bidirectional stream, which
// is what Envoy does per request.
func extProcDecide(c extProcPb.ExternalProcessorClient, path string, body []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := c.Process(ctx)
	if err != nil {
		return "", err
	}
	hdrs := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
		{Key: ":method", RawValue: []byte("POST")},
		{Key: ":path", RawValue: []byte(path)},
		{Key: "content-type", RawValue: []byte("application/json")},
	}}
	if err := stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{Headers: hdrs},
		},
	}); err != nil {
		return "", err
	}
	if err := stream.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{Body: body, EndOfStream: true},
		},
	}); err != nil {
		return "", err
	}
	if err := stream.CloseSend(); err != nil {
		return "", err
	}

	var dest string
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return dest, nil
		}
		if err != nil {
			if dest != "" {
				return dest, nil
			}
			return "", err
		}
		var common *extProcPb.CommonResponse
		switch {
		case resp.GetRequestHeaders() != nil:
			common = resp.GetRequestHeaders().GetResponse()
		case resp.GetRequestBody() != nil:
			common = resp.GetRequestBody().GetResponse()
		}
		for _, hv := range common.GetHeaderMutation().GetSetHeaders() {
			if hv.GetHeader().GetKey() == destHeader && dest == "" {
				v := string(hv.GetHeader().GetRawValue())
				if v == "" {
					v = hv.GetHeader().GetValue()
				}
				dest = v
			}
		}
	}
}

func tally(durs []time.Duration, elapsed time.Duration, n int, ok, fail []bool) bench {
	var errs, decided int
	kept := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		if fail[i] {
			errs++
			continue
		}
		if ok[i] {
			decided++
		}
		kept = append(kept, durs[i])
	}
	return summarise(kept, elapsed, n, errs, decided)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
