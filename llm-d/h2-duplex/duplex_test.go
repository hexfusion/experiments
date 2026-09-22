package main

import (
	"context"
	"testing"
	"time"
)

// TestDuplex is the question the spike exists to answer: can a next-hop policy
// service carry a response that begins before the request body completes.
func TestDuplex(t *testing.T) {
	ccfg := DefaultClientConfig()
	ucfg := DefaultUpstreamConfig()

	res, _, tl, err := Run(context.Background(), ccfg, ucfg, false, 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("status %d", res.Status)
	}
	if !res.Duplex {
		t.Fatalf("not duplex: first response %v, last request %v\n%s",
			res.FirstResponseByte, res.LastRequestByte, tl)
	}
	t.Logf("first response byte %v, last request byte %v\n%s",
		res.FirstResponseByte, res.LastRequestByte, tl)
}

// TestNonDuplexControl proves the measurement discriminates. With an upstream
// that drains the whole body before responding, the same harness must report
// no duplex.
func TestNonDuplexControl(t *testing.T) {
	ccfg := DefaultClientConfig()
	ucfg := DefaultUpstreamConfig()
	ucfg.Duplex = false

	res, _, _, err := Run(context.Background(), ccfg, ucfg, false, 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Duplex {
		t.Fatalf("expected no duplex, got first response %v before last request %v",
			res.FirstResponseByte, res.LastRequestByte)
	}
}

// TestBodyNeverMaterialized asserts the parse-once claim: bytes held at once
// stay flat as the body grows, so the policy service is not a function of B.
func TestBodyNeverMaterialized(t *testing.T) {
	ucfg := DefaultUpstreamConfig()

	small := DefaultClientConfig()
	small.ChunkBytes = 32 << 10

	large := DefaultClientConfig()
	large.ChunkBytes = 1 << 20

	_, hwSmall, _, err := Run(context.Background(), small, ucfg, false, 0)
	if err != nil {
		t.Fatalf("small: %v", err)
	}
	_, hwLarge, _, err := Run(context.Background(), large, ucfg, false, 0)
	if err != nil {
		t.Fatalf("large: %v", err)
	}

	if hwSmall != hwLarge {
		t.Fatalf("high water tracked body size: %d vs %d", hwSmall, hwLarge)
	}
	if hwLarge >= int64(large.BodyBytes()) {
		t.Fatalf("high water %d not below body size %d", hwLarge, large.BodyBytes())
	}
	t.Logf("body %d KB handled with %d KB held", large.BodyBytes()>>10, hwLarge>>10)
}

// TestHeadersNotGatedOnBody checks golang/go#17480, where the h2 transport hung
// on a request body pipe that had not been written yet. It no longer hangs, and
// headers are not gated on the body either: the upstream sees them while the
// pipe is still empty. So a policy service can forward headers and let the
// upstream start before any body byte exists, which is what makes a
// header-and-metadata-only decision path possible.
func TestHeadersNotGatedOnBody(t *testing.T) {
	const delay = 300 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, _, tl, err := Run(ctx, DefaultClientConfig(), DefaultUpstreamConfig(), false, delay)
	if err != nil {
		t.Fatalf("run (hang or error): %v", err)
	}
	got, ok := tl.Get("upstream.headers-received")
	if !ok {
		t.Fatal("upstream never received headers")
	}
	if got >= delay {
		t.Fatalf("upstream headers at %v were gated on the first body write at %v", got, delay)
	}
	t.Logf("no hang; upstream headers arrived at %v with the body pipe still empty until %v", got, delay)
}
