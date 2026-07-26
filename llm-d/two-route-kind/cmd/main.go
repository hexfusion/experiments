// One binary, two roles, so the demo builds a single image.
//
//	sim  a stand-in model server that reports which pod served the request
//	ipp  the decider: reads the body, picks a destination, hands the request
//	     back to the gateway with a marker so route 2 dispatches it
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	switch os.Getenv("MODE") {
	case "sim":
		runSim()
	case "ipp":
		runIPP()
	case "load":
		runLoad()
	case "bench":
		runBench()
	default:
		log.Fatal("set MODE to sim or ipp")
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---- sim ----

type simReply struct {
	Pod   string `json:"pod"`
	Path  string `json:"path"`
	Model string `json:"model,omitempty"`
}

func runSim() {
	pod := env("POD_NAME", "sim")
	// A vLLM engine runs a bounded batch and queues the rest. Reporting every
	// in-flight request as "running" with an always-empty queue is what a naive
	// fake does, and it makes the load-aware scorer blind: it reads
	// WaitingQueueSize, not running count.
	capacity := int64(atoiOr(env("SIM_BATCH", "8"), 8))
	var inflight atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	// The vLLM metric names EPP scrapes by default. Without these the endpoints
	// carry no load signal and load-aware scoring has nothing to work with.
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		n := inflight.Load()
		running := min(n, capacity)
		waiting := max(0, n-capacity)
		fmt.Fprintf(w, "# TYPE vllm:num_requests_running gauge\nvllm:num_requests_running{model_name=\"llama-3.1-8b\"} %d\n", running)
		fmt.Fprintf(w, "# TYPE vllm:num_requests_waiting gauge\nvllm:num_requests_waiting{model_name=\"llama-3.1-8b\"} %d\n", waiting)
		fmt.Fprintf(w, "# TYPE vllm:kv_cache_usage_perc gauge\nvllm:kv_cache_usage_perc{model_name=\"llama-3.1-8b\"} %.3f\n", float64(running)/float64(capacity))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)
		// Per-request so one pod can be held busy without redeploying the others.
		// Holding the handler is what makes num_requests_running non-zero long
		// enough for EPP to scrape it.
		if d, err := time.ParseDuration(r.Header.Get("x-sim-delay")); err == nil && d > 0 {
			time.Sleep(d)
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-served-by", pod)
		_ = json.NewEncoder(w).Encode(simReply{Pod: pod, Path: r.URL.Path, Model: req.Model})
	})
	log.Printf("sim %s listening on :8000", pod)
	log.Fatal(http.ListenAndServe(":8000", mux))
}

// ---- ipp ----

const (
	markerHeader = "x-ipp-processed"
	destHeader   = "x-gateway-destination-endpoint"
)

type ipp struct {
	gateway   string
	eppURL    string
	endpoints []string

	// Separate clients so a stall dispatching upstream cannot starve idle
	// connection slots for the decision call, and so the two can carry
	// different timeouts. One shared client with the default transport gives
	// both MaxIdleConnsPerHost=2, which reconnects on almost every request.
	epp *http.Client
	fwd *http.Client

	eppRetries int
	next       atomic.Uint64
	decisions  atomic.Int64
	retries    atomic.Int64
}

// newTransport sizes the connection pool to expected concurrency. The default
// transport allows 2 idle connections per host, so past 2 concurrent requests
// every connection is opened, used once and closed: a TCP handshake per request
// plus TIME_WAIT and ephemeral port pressure.
func newTransport(conns int) *http.Transport {
	return &http.Transport{
		MaxIdleConns:        conns * 2,
		MaxIdleConnsPerHost: conns,
		MaxConnsPerHost:     conns * 2,
		IdleConnTimeout:     90 * time.Second,
		// Nothing here benefits from gzip, and it costs a copy each way.
		DisableCompression: true,
	}
}

func runIPP() {
	conns := atoiOr(env("MAX_CONNS", "512"), 512)
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p := &ipp{
		gateway:   env("GATEWAY_URL", "http://gateway.two-route.svc.cluster.local:80"),
		eppURL:    env("EPP_URL", ""),
		endpoints: strings.Split(env("ENDPOINTS", ""), ","),
		// The decision call sits in front of the first token, so its timeout is
		// short: a slow decider should fail fast rather than hold the request.
		epp: &http.Client{
			Timeout:       durOr(env("EPP_TIMEOUT", "2s"), 2*time.Second),
			Transport:     newTransport(conns),
			CheckRedirect: noRedirect,
		},
		// The dispatch carries the generation, which is long. A redirect here
		// would be a routing loop resolving quietly, so refuse to follow one.
		fwd: &http.Client{
			Timeout:       durOr(env("FWD_TIMEOUT", "5m"), 5*time.Minute),
			Transport:     newTransport(conns),
			CheckRedirect: noRedirect,
		},
		eppRetries: atoiOr(env("EPP_RETRIES", "2"), 2),
	}
	if p.eppURL == "" && (len(p.endpoints) == 0 || p.endpoints[0] == "") {
		log.Fatal("set EPP_URL, or ENDPOINTS for the standalone round-robin fallback")
	}

	// Readiness is separate from liveness so a terminating pod leaves the
	// Service endpoints before the listener stops. Without that gap the gateway
	// keeps routing to a socket that is already closing and clients see resets
	// rather than a drain.
	var draining atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if draining.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"decisions":   p.decisions.Load(),
			"epp_retries": p.retries.Load(),
			"endpoints":   p.endpoints,
		})
	})
	mux.HandleFunc("/", p.serve)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
		// The read must outlast a large body arriving slowly; the write must
		// outlast a full generation, since this proxies the response.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// Requests carry the server's base context, so shutdown does not cancel
	// in-flight work: Shutdown waits for it, and cancelling here instead would
	// abort generations that were about to finish.
	srv.BaseContext = func(net.Listener) context.Context { return context.Background() }

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sig
		// Fail readiness first and wait long enough for kube-proxy and the
		// gateway to observe it. Shutting the listener immediately is what
		// produces connection resets during a rolling update.
		draining.Store(true)
		log.Printf("draining: failing readiness for %s before shutdown", drainDelay)
		time.Sleep(drainDelay)

		// Then stop accepting and wait for in-flight requests. The grace must
		// exceed the forward timeout or a generation in progress is cut off.
		ctx, cancel := context.WithTimeout(context.Background(), drainGrace)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		log.Printf("drained")
	}()

	log.Printf("ipp listening on :8080, gateway=%s epp=%q endpoints=%v", p.gateway, p.eppURL, p.endpoints)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

var (
	drainDelay = durOr(env("DRAIN_DELAY", "5s"), 5*time.Second)
	drainGrace = durOr(env("DRAIN_GRACE", "60s"), 60*time.Second)
)

// decision is EPP's reply on the plain-HTTP transport. No phases, no stream.
type decision struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
	Denied   *struct {
		Code int    `json:"code"`
		Body string `json:"body"`
	} `json:"denied"`
}

// pick asks EPP when one is configured, and otherwise round-robins a static
// list. The fallback exists so the routing topology can be exercised without a
// scheduler in the picture.
//
// Retrying here is safe in a way retrying the dispatch is not: nothing has been
// forwarded yet, so a second attempt has no side effect on a model server. The
// caveat is EPP's own bookkeeping, which runs admission control per consult, so
// a retried decision is counted twice. The request id is carried so a decider
// that wants to dedupe can.
func (p *ipp) pick(ctx context.Context, reqID, path string, body []byte) (string, string, error) {
	if p.eppURL == "" {
		return p.endpoints[int(p.next.Add(1)-1)%len(p.endpoints)], "round-robin", nil
	}

	var lastErr error
	for attempt := 0; attempt <= p.eppRetries; attempt++ {
		if attempt > 0 {
			p.retries.Add(1)
			// Short backoff: this is in front of the first token, so a long
			// wait defeats the point of retrying at all.
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 20 * time.Millisecond):
			}
		}
		dest, retryable, err := p.askEPP(ctx, reqID, path, body)
		if err == nil {
			return dest, "epp", nil
		}
		lastErr = err
		if !retryable {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("epp: %d attempts: %w", p.eppRetries+1, lastErr)
}

// askEPP performs one decision call, reporting whether a failure is worth
// another attempt.
func (p *ipp) askEPP(ctx context.Context, reqID, path string, body []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.eppURL+path, bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", reqID)
	req.ContentLength = int64(len(body))

	resp, err := p.epp.Do(req)
	if err != nil {
		// A caller that gave up is not a transient decider failure.
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", true, fmt.Errorf("epp: %w", err)
	}
	defer drainClose(resp.Body)

	// 5xx is the decider failing; 4xx is this request being wrong, and a second
	// identical attempt will be wrong the same way.
	if resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("epp status %d", resp.StatusCode)
	}

	var d decision
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", false, fmt.Errorf("epp decode: %w", err)
	}
	if d.Denied != nil {
		return "", false, fmt.Errorf("epp denied %d: %s", d.Denied.Code, d.Denied.Body)
	}
	if d.Endpoint == "" {
		return "", false, fmt.Errorf("epp returned no endpoint (status %d)", resp.StatusCode)
	}
	return d.Endpoint, false, nil
}

// drainClose reads any remainder before closing so net/http can reuse the
// connection. json.Decoder stops at the end of the value, so without this the
// body's stored error is not io.EOF and the transport retires the connection,
// costing a TCP handshake on every single decision.
func drainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 1<<16))
	_ = rc.Close()
}

// Hop-by-hop headers must not be forwarded. An inbound Connection: close would
// otherwise make the transport tear down the upstream connection every request,
// invisibly undoing the pooling above. RFC 7230 section 6.1.
var hopByHop = map[string]bool{
	"Connection": true, "Proxy-Connection": true, "Keep-Alive": true,
	"Proxy-Authenticate": true, "Proxy-Authorization": true, "Te": true,
	"Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

func durOr(s string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

// serve is the whole of route 1's backend: read the body once, decide, and hand
// the request back to the gateway marked so route 2 wins on match precedence.
func (p *ipp) serve(w http.ResponseWriter, r *http.Request) {
	// Arriving marked means route 2 did not take precedence. Fail loudly: a
	// silent loop is the failure mode this topology has to rule out.
	if r.Header.Get(markerHeader) != "" {
		http.Error(w, "loop: marked request reached route 1", http.StatusLoopDetected)
		return
	}

	// Presize from Content-Length rather than letting io.ReadAll double its way
	// there; this is the same fix the shim needed on the server side.
	body, err := readAllSized(r, 32<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reqID := r.Header.Get("x-request-id")
	if reqID == "" {
		reqID = fmt.Sprintf("ipp-%d", p.decisions.Load())
	}

	dest, by, err := p.pick(r.Context(), reqID, r.URL.Path, body)
	if err != nil {
		// A decider that cannot answer is not a reason to guess: failing here
		// is what keeps a misconfigured chain from looking like a working one.
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	p.decisions.Add(1)

	out, err := http.NewRequestWithContext(r.Context(), r.Method, p.gateway+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.ContentLength = int64(len(body))
	for k, vs := range r.Header {
		if hopByHop[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	out.Header.Set(markerHeader, "1")
	out.Header.Set(destHeader, dest)
	out.Header.Set("x-decided-by", env("POD_NAME", "ipp"))
	out.Header.Set("x-decided-how", by)

	// Deliberately not retried. Unlike the decision call, this may already have
	// reached a model server, and a blind second attempt would bill and generate
	// twice. Re-dispatch belongs to whoever knows the first attempt never
	// arrived, which is the data plane, and it should re-consult for a fresh
	// decision rather than reuse this one: the endpoint that just failed is the
	// one this decision names.
	resp, err := p.fwd.Do(out)
	if err != nil {
		http.Error(w, fmt.Sprintf("dispatch to %s failed: %v", dest, err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("x-ipp-destination", dest)
	// On the response too, so a caller can see which replica decided. Without it
	// the spread across a scaled IPP is invisible from outside.
	w.Header().Set("x-decided-by", env("POD_NAME", "ipp"))
	w.Header().Set("x-decided-how", by)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ---- load ----

type result struct {
	pod, ipp, dest string
	status         int
	dur            time.Duration
	err            error
}

// runLoad drives the gateway from inside the cluster, so the measurement is not
// shaped by a port-forward.
func runLoad() {
	var (
		gw     = env("GATEWAY_URL", "http://demo-istio.two-route.svc.cluster.local:80")
		path   = env("PATH_", "/v1/chat/completions")
		n      = atoiOr(env("REQUESTS", "2000"), 2000)
		conc   = atoiOr(env("CONCURRENCY", "200"), 200)
		delay  = env("DELAY", "")
		bodyKB = atoiOr(env("BODY_KB", "0"), 0)
		client = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        conc * 2,
				MaxIdleConnsPerHost: conc * 2,
				MaxConnsPerHost:     0,
			},
		}
	)
	log.Printf("load: %d requests, concurrency %d, delay %q, target %s%s", n, conc, delay, gw, path)

	body := chatBody(bodyKB)
	log.Printf("request body: %d bytes", len(body))

	results := make([]result, n)
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
			req, _ := http.NewRequest("POST", gw+path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if delay != "" {
				req.Header.Set("x-sim-delay", delay)
			}
			resp, err := client.Do(req)
			r := result{dur: time.Since(t0)}
			if err != nil {
				r.err = err
				results[i] = r
				return
			}
			var sr simReply
			_ = json.NewDecoder(resp.Body).Decode(&sr)
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			r.status = resp.StatusCode
			r.pod = sr.Pod
			r.ipp = resp.Header.Get("x-decided-by")
			r.dest = resp.Header.Get("x-ipp-destination")
			results[i] = r
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	byPod, byIPP, byStatus := map[string]int{}, map[string]int{}, map[int]int{}
	var errs int
	durs := make([]time.Duration, 0, n)
	for _, r := range results {
		if r.err != nil {
			errs++
			continue
		}
		byStatus[r.status]++
		durs = append(durs, r.dur)
		if r.status == 200 {
			byPod[r.pod]++
			byIPP[r.ipp]++
		}
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })

	fmt.Printf("\n=== %d requests, concurrency %d, %.1fs, %.0f req/s ===\n",
		n, conc, elapsed.Seconds(), float64(n)/elapsed.Seconds())
	fmt.Printf("transport errors : %d\n", errs)
	fmt.Printf("status           : %v\n", byStatus)
	if len(durs) > 0 {
		fmt.Printf("latency p50/p99  : %v / %v\n", durs[len(durs)/2], durs[len(durs)*99/100])
	}
	fmt.Printf("served by sim    : %v\n", byPod)
	fmt.Printf("decided by ipp   : %v\n", byIPP)
	if len(byPod) < 2 {
		fmt.Printf("\nWARNING: traffic did not spread across sims\n")
	}
}

// chatBody builds a chat completion of roughly kb kilobytes. Size matters here
// because the body crosses the gateway twice, IPP reads all of it, and EPP
// parses it, so it is the axis the two-route topology should be worst on.
func chatBody(kb int) []byte {
	msgs := []map[string]string{{"role": "system", "content": "you are a helpful assistant"}}
	if kb > 0 {
		turn := strings.Repeat("the quick brown fox jumps over the lazy dog ", 24) // ~1KB
		for i := 0; i < kb; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			msgs = append(msgs, map[string]string{"role": role, "content": itoa(i) + " " + turn})
		}
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": "summarise the conversation"})
	b, err := json.Marshal(map[string]any{"model": "llama-3.1-8b", "messages": msgs})
	if err != nil {
		panic(err)
	}
	return b
}

func itoa(i int) string { return strconv.Itoa(i) }

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// readAllSized reads a request body into a right-sized buffer. io.ReadAll grows
// by doubling, which is the same waste the shim needed fixing for on the server
// side. Truncates to bytes actually read: Content-Length is a client-supplied
// hint and must not widen the slice.
func readAllSized(r *http.Request, max int64) ([]byte, error) {
	lr := io.LimitReader(r.Body, max)
	n := r.ContentLength
	if n <= 0 || n > max {
		return io.ReadAll(lr)
	}
	buf := make([]byte, n)
	nr, err := io.ReadFull(lr, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:nr], nil
}
