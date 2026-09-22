package main

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"sync"
	"time"
)

// EPP is one decider replica behind a plain HTTP handler.
//
// Two things it demonstrates. Its belief about pod load is only as good as its
// last scrape plus what it has been told since, which is where replicas
// diverge. And it can decide from published metadata alone, never seeing the
// body, which is the property worth keeping from the ext_proc contract.
type EPP struct {
	Name string
	Pods []*Pod

	bus       *Bus
	broadcast bool

	mu        sync.Mutex
	inflight  map[string]int
	prefix    map[uint64]string // prefix hash to the pod that served it last
	decisions int
	bodyBytes int64 // bytes of raw body this replica had to receive
	parses    int   // times it had to derive attributes itself
}

func NewEPP(name string, pods []*Pod, bus *Bus, broadcast bool) *EPP {
	e := &EPP{
		Name: name, Pods: pods, bus: bus, broadcast: broadcast,
		inflight: map[string]int{}, prefix: map[uint64]string{},
	}
	for _, p := range pods {
		e.inflight[p.Name] = 0
	}
	go e.consume(bus.Subscribe())
	return e
}

// consume applies peer decisions. Skipping our own avoids double counting what
// schedule already applied locally.
func (e *EPP) consume(ch <-chan Event) {
	for ev := range ch {
		if ev.Source == e.Name {
			continue
		}
		e.mu.Lock()
		switch ev.Kind {
		case Started:
			e.inflight[ev.Endpoint]++
			if ev.PrefixHash != 0 {
				e.prefix[ev.PrefixHash] = ev.Endpoint
			}
		case Finished:
			e.inflight[ev.Endpoint]--
		}
		e.mu.Unlock()
	}
}

// Scrape replaces the estimate with truth, the way pulling vLLM metrics does.
// Everything between two scrapes is inference from local decisions.
func (e *EPP) Scrape() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.Pods {
		e.inflight[p.Name] = p.Inflight()
	}
}

func (e *EPP) ScrapeLoop(stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			e.Scrape()
		}
	}
}

// pick is load-aware scoring plus prefix affinity, reduced to essentials. A warm
// pod wins unless it is more than one request busier than the least loaded, so
// affinity yields to load rather than overriding it.
func (e *EPP) pick(attr *Attributes) string {
	least, leastN := e.Pods[0].Name, e.inflight[e.Pods[0].Name]
	for _, p := range e.Pods[1:] {
		if n := e.inflight[p.Name]; n < leastN {
			least, leastN = p.Name, n
		}
	}
	if attr != nil && attr.PrefixHash != 0 {
		if warm, ok := e.prefix[attr.PrefixHash]; ok && e.inflight[warm] <= leastN+1 {
			return warm
		}
	}
	return least
}

// deriveAttributes is the work a caller spares us by publishing metadata. It
// stands in for tokenize plus block hashing.
func (e *EPP) deriveAttributes(body []byte) *Attributes {
	h := fnv.New64a()
	if n := len(body); n > 512 {
		h.Write(body[:512]) // prefix, not whole body: affinity is about the head
	} else {
		h.Write(body)
	}
	return &Attributes{PromptTokens: len(body) / 4, PrefixHash: h.Sum64()}
}

func (e *EPP) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/schedule", e.schedule)
	mux.HandleFunc("POST /v1/report", e.report)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (e *EPP) schedule(w http.ResponseWriter, r *http.Request) {
	var req ScheduleRequest

	// Headers with no entity is the carrier that works in both topologies, so it
	// is tried first. Anything else arrives as a document.
	if r.Header.Get(HdrRequestID) != "" {
		var err error
		req.RequestID, req.Model, req.Attributes, err = DecodeAttributes(r.Header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	attr := req.Attributes
	e.mu.Lock()
	if attr == nil && len(req.Body) > 0 {
		e.bodyBytes += int64(len(req.Body))
		e.parses++
		e.mu.Unlock()
		attr = e.deriveAttributes(req.Body)
		e.mu.Lock()
	}
	ep := e.pick(attr)
	e.inflight[ep]++
	e.decisions++
	if attr != nil && attr.PrefixHash != 0 {
		e.prefix[attr.PrefixHash] = ep
	}
	e.mu.Unlock()

	if e.broadcast {
		ev := Event{Kind: Started, Source: e.Name, RequestID: req.RequestID, Endpoint: ep}
		if attr != nil {
			ev.PrefixHash = attr.PrefixHash
		}
		e.bus.Publish(ev)
	}

	writeJSON(w, ScheduleResponse{
		Endpoint:   ep,
		SetHeaders: map[string]string{"x-decided-by": e.Name},
	})
}

func (e *EPP) report(w http.ResponseWriter, r *http.Request) {
	var rep Report
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	e.mu.Lock()
	e.inflight[rep.Endpoint]--
	e.mu.Unlock()

	if e.broadcast {
		e.bus.Publish(Event{Kind: Finished, Source: e.Name, RequestID: rep.RequestID, Endpoint: rep.Endpoint})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e *EPP) Decisions() int   { e.mu.Lock(); defer e.mu.Unlock(); return e.decisions }
func (e *EPP) BodyBytes() int64 { e.mu.Lock(); defer e.mu.Unlock(); return e.bodyBytes }
func (e *EPP) Parses() int      { e.mu.Lock(); defer e.mu.Unlock(); return e.parses }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
