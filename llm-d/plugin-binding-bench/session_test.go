package main

import (
	"context"
	"testing"
)

// TestDeltaMatchesFull is the correctness gate on delta extraction. A session
// cache that can disagree with a full extract is a new instance of exactly the
// bug this design exists to prevent, so the speedup is only interesting if the
// results are identical.
func TestDeltaMatchesFull(t *testing.T) {
	conv := DefaultConversation()
	conv.Turns = 12
	reqs := conv.Requests()

	full := NewExtractor(nil)
	delta := NewExtractor(nil)

	for turn, body := range reqs {
		want, err := full.Extract(body)
		if err != nil {
			t.Fatalf("turn %d full: %v", turn, err)
		}
		got, used, err := delta.ExtractSession(context.Background(), "s1", body)
		if err != nil {
			t.Fatalf("turn %d delta: %v", turn, err)
		}
		if turn > 0 && !used {
			t.Fatalf("turn %d took the slow path unexpectedly", turn)
		}
		if got.TokenCount != want.TokenCount {
			t.Fatalf("turn %d token count: delta %d, full %d", turn, got.TokenCount, want.TokenCount)
		}
		if len(got.BlockKeys) != len(want.BlockKeys) {
			t.Fatalf("turn %d block count: delta %d, full %d", turn, len(got.BlockKeys), len(want.BlockKeys))
		}
		for i := range want.BlockKeys {
			if got.BlockKeys[i] != want.BlockKeys[i] {
				t.Fatalf("turn %d block %d differs: delta %x, full %x", turn, i, got.BlockKeys[i], want.BlockKeys[i])
			}
		}
		for i := range want.Tokens {
			if got.Tokens[i] != want.Tokens[i] {
				t.Fatalf("turn %d token %d differs", turn, i)
			}
		}
	}
}

// TestDeltaAcrossReplicas checks the active-active case: a replica that missed
// intervening turns must still produce the same answer, because the request
// carries the whole history and it can render the gap.
func TestDeltaAcrossReplicas(t *testing.T) {
	conv := DefaultConversation()
	conv.Turns = 12
	reqs := conv.Requests()

	full := NewExtractor(nil)
	a, b := NewExtractor(nil), NewExtractor(nil)

	for turn, body := range reqs {
		want, err := full.Extract(body)
		if err != nil {
			t.Fatal(err)
		}
		e := a
		if turn%2 == 1 {
			e = b
		}
		got, _, err := e.ExtractSession(context.Background(), "s1", body)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		if got.TokenCount != want.TokenCount || len(got.BlockKeys) != len(want.BlockKeys) {
			t.Fatalf("turn %d diverged: delta %d/%d, full %d/%d",
				turn, got.TokenCount, len(got.BlockKeys), want.TokenCount, len(want.BlockKeys))
		}
		for i := range want.BlockKeys {
			if got.BlockKeys[i] != want.BlockKeys[i] {
				t.Fatalf("turn %d block %d differs across replicas", turn, i)
			}
		}
	}
}
