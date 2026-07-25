package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Load is a concurrent driver: C sessions in flight, each replaying a full
// multi-turn conversation against one target.
type Load struct {
	Target      string
	Concurrency int
	Sessions    int
	Requests    [][]byte
	// H2C drives the target with cleartext HTTP/2 instead of HTTP/1.1.
	H2C bool
}

// LoadResult carries the latency distribution rather than a mean, because the
// question under load is the tail.
type LoadResult struct {
	Label   string
	Count   int
	Errors  int
	Elapsed time.Duration
	P50     time.Duration
	P90     time.Duration
	P99     time.Duration
	Max     time.Duration
	RPS     float64
}

func RunLoad(ctx context.Context, label string, l Load) (LoadResult, error) {
	var rt http.RoundTripper = &http.Transport{
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 1024,
		IdleConnTimeout:     90 * time.Second,
	}
	if l.H2C {
		rt = newH2CTransport()
	}
	client := &http.Client{Timeout: 120 * time.Second, Transport: rt}

	type sample struct {
		d   time.Duration
		err bool
	}
	sessions := make(chan int, l.Sessions)
	for i := 0; i < l.Sessions; i++ {
		sessions <- i
	}
	close(sessions)

	var mu sync.Mutex
	samples := make([]sample, 0, l.Sessions*len(l.Requests))

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < l.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]sample, 0, 64)
			for range sessions {
				for _, body := range l.Requests {
					t0 := time.Now()
					err := doOne(ctx, client, l.Target, body)
					local = append(local, sample{d: time.Since(t0), err: err != nil})
				}
			}
			mu.Lock()
			samples = append(samples, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	res := LoadResult{Label: label, Count: len(samples), Elapsed: elapsed}
	ds := make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		if s.err {
			res.Errors++
			continue
		}
		ds = append(ds, s.d)
	}
	if len(ds) == 0 {
		return res, fmt.Errorf("%s: all %d requests failed", label, res.Errors)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	res.P50 = ds[len(ds)*50/100]
	res.P90 = ds[min(len(ds)*90/100, len(ds)-1)]
	res.P99 = ds[min(len(ds)*99/100, len(ds)-1)]
	res.Max = ds[len(ds)-1]
	res.RPS = float64(len(ds)) / elapsed.Seconds()
	return res, nil
}

func doOne(ctx context.Context, client *http.Client, target string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
