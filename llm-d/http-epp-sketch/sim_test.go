package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type rig struct {
	pods []*Pod
	epps []*EPP
	ipp  *IPP
	stop chan struct{}
	srvs []*httptest.Server
}

func (r *rig) close() {
	close(r.stop)
	for _, s := range r.srvs {
		s.Close()
	}
}

// newRig stands up real HTTP servers, one per replica, so active-active is
// exercised over the wire rather than through function calls.
func newRig(t *testing.T, replicas, npods int, broadcast bool, carrier string, scrape time.Duration) *rig {
	t.Helper()

	bus := NewBus()
	r := &rig{stop: make(chan struct{})}

	byName := map[string]*Pod{}
	for i := 0; i < npods; i++ {
		p := &Pod{Name: fmt.Sprintf("pod-%d", i)}
		r.pods = append(r.pods, p)
		byName[p.Name] = p
	}

	var reps []Replica
	for i := 0; i < replicas; i++ {
		e := NewEPP(fmt.Sprintf("epp-%d", i), r.pods, bus, broadcast)
		srv := httptest.NewServer(e.Handler())
		go e.ScrapeLoop(r.stop, scrape)
		r.epps = append(r.epps, e)
		r.srvs = append(r.srvs, srv)
		reps = append(reps, Replica{Name: e.Name, URL: srv.URL})
	}

	r.ipp = &IPP{
		EPPs:            reps,
		Pods:            byName,
		Client:          &http.Client{Timeout: 5 * time.Second},
		Work:            40 * time.Millisecond,
		Carrier:         carrier,
		ReportToDecider: !broadcast,
	}
	return r
}

// body is a realistic chat completion. It must be valid JSON: the raw arm puts
// it in a json.RawMessage, which is exactly how a real caller would forward it.
func body(prefix int) []byte {
	b, err := json.Marshal(map[string]any{
		"model": "llama-3.1-8b",
		"messages": []map[string]string{{
			"role":    "system",
			"content": fmt.Sprintf("conversation-%d %s", prefix, strings.Repeat("token ", 600)),
		}},
	})
	if err != nil {
		panic(err)
	}
	return b
}

// drive issues n requests with the given concurrency.
func (r *rig) drive(t *testing.T, n, conc int) {
	t.Helper()
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := r.ipp.Handle(fmt.Sprintf("req-%d", i), "llama-3.1-8b", body(i%8)); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d request errors, first: %v", len(errs), errs[0])
	}
}

func (r *rig) peak() int {
	m := 0
	for _, p := range r.pods {
		if v := p.Peak(); v > m {
			m = v
		}
	}
	return m
}

// TestActiveActive confirms plain HTTP round-robin actually reaches every
// replica. It is the claim that the whole shape rests on.
func TestActiveActive(t *testing.T) {
	r := newRig(t, 3, 4, true, CarrierHeaders, 50*time.Millisecond)
	defer r.close()

	r.drive(t, 90, 12)

	for _, e := range r.epps {
		if e.Decisions() == 0 {
			t.Errorf("%s made no decisions: traffic did not spread", e.Name)
		}
		t.Logf("%s decisions=%d", e.Name, e.Decisions())
	}
}

// TestDivergence is the point of the sketch. Replicas that only see their own
// decisions herd onto whichever pod looked idle at the last scrape.
func TestDivergence(t *testing.T) {
	// Scrape longer than the run, so this measures pure inter-scrape behaviour:
	// what a replica believes when all it has is its own decisions.
	const scrape = 10 * time.Second
	const replicas, pods, conc, n = 6, 8, 24, 240

	off := newRig(t, replicas, pods, false, CarrierHeaders, scrape)
	off.drive(t, n, conc)
	peakOff, spreadOff := off.peak(), off.served()
	off.close()

	on := newRig(t, replicas, pods, true, CarrierHeaders, scrape)
	on.drive(t, n, conc)
	peakOn, spreadOn := on.peak(), on.served()
	on.close()

	ideal := conc / pods
	t.Logf("%d replicas, %d pods, concurrency %d (ideal peak %d)", replicas, pods, conc, ideal)
	t.Logf("broadcast off: peak=%d  per-pod=%v", peakOff, spreadOff)
	t.Logf("broadcast on:  peak=%d  per-pod=%v", peakOn, spreadOn)

	if peakOn >= peakOff {
		t.Errorf("broadcast did not reduce peak load: off=%d on=%d", peakOff, peakOn)
	}
}

// TestMetadataPath is the property to protect: the decider decides without ever
// receiving the body. Three carriers, same decision quality.
func TestMetadataPath(t *testing.T) {
	type stat struct {
		sent   int64
		body   int64
		parses int
	}
	run := func(carrier string) stat {
		r := newRig(t, 3, 4, true, carrier, 50*time.Millisecond)
		r.drive(t, 90, 12)
		st := stat{sent: r.ipp.SentBytes()}
		for _, e := range r.epps {
			st.body += e.BodyBytes()
			st.parses += e.Parses()
		}
		r.close()
		return st
	}

	raw := run(CarrierBody)
	js := run(CarrierJSON)
	hd := run(CarrierHeaders)

	t.Logf("body    : %7d bytes to EPP, %6d bytes of body received, %d parses", raw.sent, raw.body, raw.parses)
	t.Logf("json    : %7d bytes to EPP, %6d bytes of body received, %d parses", js.sent, js.body, js.parses)
	t.Logf("headers : %7d bytes to EPP, %6d bytes of body received, %d parses", hd.sent, hd.body, hd.parses)
	t.Logf("headers is %.1fx cheaper than forwarding the body, %.2fx vs a json document",
		float64(raw.sent)/float64(hd.sent), float64(js.sent)/float64(hd.sent))

	for name, st := range map[string]stat{"json": js, "headers": hd} {
		if st.body != 0 || st.parses != 0 {
			t.Errorf("%s carrier still moved body: bytes=%d parses=%d", name, st.body, st.parses)
		}
	}
	if hd.sent >= raw.sent {
		t.Errorf("headers carrier sent no fewer bytes than the body: %d vs %d", hd.sent, raw.sent)
	}
}

func (r *rig) served() []int {
	out := make([]int, 0, len(r.pods))
	for _, p := range r.pods {
		out = append(out, p.Served())
	}
	return out
}

// TestHeaderValidation covers the values that originate in the user's request
// body and end up in a header. Rejecting beats encoding: base64 would carry the
// same garbage safely rather than refusing it.
func TestHeaderValidation(t *testing.T) {
	cases := []struct {
		name, reqID, model string
		ok                 bool
	}{
		{"ordinary", "req-1", "meta-llama/Llama-3.1-8B-Instruct", true},
		{"dots and colons", "req-1", "registry.io/org/model:v1.2", true},
		{"crlf in model", "req-1", "llama\r\nx-injected: 1", false},
		{"crlf in request id", "r\r\nx: 1", "llama", false},
		{"space in model", "req-1", "llama 3", false},
		{"empty model", "req-1", "", false},
		{"oversized model", "req-1", strings.Repeat("m", maxModelLen+1), false},
		{"null byte", "req-1", "llama\x00", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			err := EncodeAttributes(h, c.reqID, c.model, &Attributes{PromptTokens: 10, PrefixHash: 42})
			if c.ok && err != nil {
				t.Fatalf("rejected a legitimate value: %v", err)
			}
			if !c.ok {
				if err == nil {
					t.Fatal("accepted a value that has no business in a header")
				}
				return
			}
			// The decider validates again rather than trusting its caller.
			gotID, gotModel, attr, err := DecodeAttributes(h)
			if err != nil {
				t.Fatalf("decode rejected what encode accepted: %v", err)
			}
			if gotID != c.reqID || gotModel != c.model || attr == nil {
				t.Fatalf("round trip lost data: id=%q model=%q attr=%v", gotID, gotModel, attr)
			}
		})
	}
}

// TestForgedHeadersRejected is the case a caller cannot cause: headers arriving
// already populated with a hostile value.
func TestForgedHeadersRejected(t *testing.T) {
	h := http.Header{}
	h[HdrRequestID] = []string{"req-1"}
	h[HdrModel] = []string{"llama\r\nx-gateway-destination-endpoint: 10.0.0.1:8000"}

	if _, _, _, err := DecodeAttributes(h); err == nil {
		t.Fatal("decoder accepted a forged model header")
	}
}

// TestMalformedAttributesDegrade separates identity from hints. A bad hash costs
// affinity; a bad identity has nothing to fall back to.
func TestMalformedAttributesDegrade(t *testing.T) {
	h := http.Header{}
	h.Set(HdrRequestID, "req-1")
	h.Set(HdrModel, "llama")
	h.Set(HdrPrefixHash, "not-a-hash")

	id, model, attr, err := DecodeAttributes(h)
	if err != nil {
		t.Fatalf("malformed attributes should degrade, not fail: %v", err)
	}
	if id != "req-1" || model != "llama" {
		t.Fatalf("identity lost: id=%q model=%q", id, model)
	}
	if attr != nil {
		t.Fatal("expected attributes to be dropped so routing falls back to load-only")
	}
}

// TestDivergenceRealisticRatio re-runs the divergence case at the ratio real
// serving has. What matters is scrape interval over request duration: EPP
// refreshes every 50ms by default and LLM requests run seconds, so roughly
// 1:100. TestDivergence deliberately used 250:1 to isolate the effect, which
// exaggerates it.
func TestDivergenceRealisticRatio(t *testing.T) {
	const replicas, pods, conc, n = 6, 8, 24, 240
	const work = 400 * time.Millisecond
	const scrape = 4 * time.Millisecond // 1:100, as 50ms is to 5s

	run := func(broadcast bool) (int, []int) {
		r := newRig(t, replicas, pods, broadcast, CarrierHeaders, scrape)
		r.ipp.Work = work
		r.drive(t, n, conc)
		p, s := r.peak(), r.served()
		r.close()
		return p, s
	}

	peakOff, spreadOff := run(false)
	peakOn, spreadOn := run(true)

	t.Logf("scrape:work = 1:%d (default 50ms against a multi-second request)", int(work/scrape))
	t.Logf("broadcast off: peak=%d  per-pod=%v", peakOff, spreadOff)
	t.Logf("broadcast on:  peak=%d  per-pod=%v", peakOn, spreadOn)
	t.Logf("ideal peak %d", conc/pods)
}
