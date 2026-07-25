package main

import (
	"sync"
	"time"
)

// Pod stands in for a model server. Only its concurrency matters here: it is the
// ground truth every decider is trying to estimate between scrapes.
type Pod struct {
	Name string

	mu       sync.Mutex
	inflight int
	peak     int
	served   int
}

func (p *Pod) Serve(d time.Duration) {
	p.mu.Lock()
	p.inflight++
	p.served++
	if p.inflight > p.peak {
		p.peak = p.inflight
	}
	p.mu.Unlock()

	time.Sleep(d)

	p.mu.Lock()
	p.inflight--
	p.mu.Unlock()
}

func (p *Pod) Inflight() int { p.mu.Lock(); defer p.mu.Unlock(); return p.inflight }
func (p *Pod) Peak() int     { p.mu.Lock(); defer p.mu.Unlock(); return p.peak }
func (p *Pod) Served() int   { p.mu.Lock(); defer p.mu.Unlock(); return p.served }
