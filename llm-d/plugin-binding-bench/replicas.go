package main

import (
	"context"
	"hash/maphash"
	"sync/atomic"
)

// ReplicaSet models the deployment that actually exists: several shim replicas,
// active-active, each with its own session cache and no shared state.
//
// The question delta extraction has to survive is what happens when a session's
// turns land on different replicas. Note that a replica is not restricted to
// continuing from the immediately preceding turn: multi-turn chat resends the
// whole history, so any replica holding state at message count M can extend to
// any later turn. It renders the gap rather than one turn, so cost scales with
// how far behind that replica was, not with the whole conversation.
type ReplicaSet struct {
	replicas []*Extractor
	affinity bool
	seed     maphash.Seed
	rr       atomic.Uint64

	hits   atomic.Int64
	misses atomic.Int64
}

func NewReplicaSet(n int, affinity bool, mk func() *Extractor) *ReplicaSet {
	rs := &ReplicaSet{affinity: affinity, seed: maphash.MakeSeed()}
	for i := 0; i < n; i++ {
		rs.replicas = append(rs.replicas, mk())
	}
	return rs
}

// pick chooses a replica. With affinity the session hashes to one replica, which
// is what a session-affinity scorer or consistent hashing buys. Without it the
// gateway spreads requests, which is the default today.
func (rs *ReplicaSet) pick(sessionID string, turn int) *Extractor {
	if len(rs.replicas) == 1 {
		return rs.replicas[0]
	}
	if rs.affinity {
		h := maphash.String(rs.seed, sessionID)
		return rs.replicas[h%uint64(len(rs.replicas))]
	}
	// Spread: hash session and turn together so assignment is effectively
	// random per request rather than a predictable rotation.
	h := maphash.String(rs.seed, sessionID+":"+itoa(turn))
	return rs.replicas[h%uint64(len(rs.replicas))]
}

func (rs *ReplicaSet) Extract(ctx context.Context, sessionID string, turn int, body []byte) (*Metadata, error) {
	e := rs.pick(sessionID, turn)
	m, delta, err := e.ExtractSession(ctx, sessionID, body)
	if err != nil {
		return nil, err
	}
	if delta {
		rs.hits.Add(1)
	} else {
		rs.misses.Add(1)
	}
	return m, nil
}

func (rs *ReplicaSet) Stats() (hits, misses int64) {
	return rs.hits.Load(), rs.misses.Load()
}
