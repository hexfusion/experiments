// One binary, two roles, so the demo builds a single image.
//
//	sim  a stand-in model server that reports which pod served the request
//	ipp  the decider: reads the body, picks a destination, hands the request
//	     back to the gateway with a marker so route 2 dispatches it
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	var running, waiting atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	// The vLLM metric names EPP scrapes by default. Without these the endpoints
	// carry no load signal and load-aware scoring has nothing to work with.
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "# TYPE vllm:num_requests_running gauge\nvllm:num_requests_running{model_name=\"llama-3.1-8b\"} %d\n", running.Load())
		fmt.Fprintf(w, "# TYPE vllm:num_requests_waiting gauge\nvllm:num_requests_waiting{model_name=\"llama-3.1-8b\"} %d\n", waiting.Load())
		fmt.Fprintf(w, "# TYPE vllm:kv_cache_usage_perc gauge\nvllm:kv_cache_usage_perc{model_name=\"llama-3.1-8b\"} %.3f\n", float64(running.Load())/64.0)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		running.Add(1)
		defer running.Add(-1)
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
	client    *http.Client
	next      atomic.Uint64
	decisions atomic.Int64
}

func runIPP() {
	p := &ipp{
		gateway:   env("GATEWAY_URL", "http://gateway.two-route.svc.cluster.local:80"),
		eppURL:    env("EPP_URL", ""),
		endpoints: strings.Split(env("ENDPOINTS", ""), ","),
		// No redirects: a loop must fail loudly rather than resolve quietly.
		client: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if p.eppURL == "" && (len(p.endpoints) == 0 || p.endpoints[0] == "") {
		log.Fatal("set EPP_URL, or ENDPOINTS for the standalone round-robin fallback")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"decisions": p.decisions.Load(),
			"endpoints": p.endpoints,
		})
	})
	mux.HandleFunc("/", p.serve)

	log.Printf("ipp listening on :8080, gateway=%s epp=%q endpoints=%v", p.gateway, p.eppURL, p.endpoints)
	log.Fatal(http.ListenAndServe(":8080", mux))
}

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
func (p *ipp) pick(path string, body []byte) (string, string, error) {
	if p.eppURL == "" {
		return p.endpoints[int(p.next.Add(1)-1)%len(p.endpoints)], "round-robin", nil
	}

	req, err := http.NewRequest("POST", p.eppURL+path, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("epp: %w", err)
	}
	defer resp.Body.Close()

	var d decision
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", "", fmt.Errorf("epp decode: %w", err)
	}
	if d.Denied != nil {
		return "", "", fmt.Errorf("epp denied %d: %s", d.Denied.Code, d.Denied.Body)
	}
	if d.Endpoint == "" {
		return "", "", fmt.Errorf("epp returned no endpoint (status %d)", resp.StatusCode)
	}
	return d.Endpoint, "epp", nil
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dest, by, err := p.pick(r.URL.Path, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	p.decisions.Add(1)

	out, err := http.NewRequestWithContext(r.Context(), r.Method, p.gateway+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	out.Header.Set(markerHeader, "1")
	out.Header.Set(destHeader, dest)
	out.Header.Set("x-decided-by", env("POD_NAME", "ipp"))
	out.Header.Set("x-decided-how", by)

	resp, err := p.client.Do(out)
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
		client = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        conc * 2,
				MaxIdleConnsPerHost: conc * 2,
				MaxConnsPerHost:     0,
			},
		}
	)
	log.Printf("load: %d requests, concurrency %d, target %s%s", n, conc, gw, path)

	body, _ := json.Marshal(map[string]any{
		"model":    "llama-3.1-8b",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})

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

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
