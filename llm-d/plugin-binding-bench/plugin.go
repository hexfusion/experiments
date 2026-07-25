package main

import (
	"encoding/json"
	"hash/maphash"
)

// Plugin is the logic under test. It is written once and runs identically in
// every arm; only how it receives Metadata differs. It never sees the body.
type Plugin interface {
	Name() string
	Decide(m *Metadata) Decision
}

type Decision struct {
	Endpoint string
	Score    float64
}

// Endpoint is a candidate the scorer ranks.
type Endpoint struct {
	Name        string
	QueueDepth  int
	KVUtil      float64
	PrefixBlocks []uint64
}

// scorer models an EPP-shaped scorer: rank every endpoint by load and by prefix
// overlap against the request's block keys. Work is proportional to endpoints
// times blocks, which is where a real scorer's cost lives.
type scorer struct {
	name      string
	endpoints []Endpoint
}

func NewScorer(name string, endpoints int, blocksPerEndpoint int) Plugin {
	var h maphash.Seed = maphash.MakeSeed()
	eps := make([]Endpoint, endpoints)
	for i := range eps {
		blocks := make([]uint64, blocksPerEndpoint)
		for j := range blocks {
			blocks[j] = maphash.String(h, string(rune('a'+i%26))+string(rune('a'+j%26)))
		}
		eps[i] = Endpoint{
			Name:         "pod-" + itoa(i),
			QueueDepth:   i % 17,
			KVUtil:       float64(i%100) / 100.0,
			PrefixBlocks: blocks,
		}
	}
	return &scorer{name: name, endpoints: eps}
}

func (s *scorer) Name() string { return s.name }

func (s *scorer) Decide(m *Metadata) Decision {
	best := Decision{Score: -1}
	for i := range s.endpoints {
		ep := &s.endpoints[i]
		score := (1.0 - ep.KVUtil) - float64(ep.QueueDepth)*0.01
		// Prefix overlap against the request's block keys.
		hits := 0
		for _, want := range m.BlockKeys {
			for _, have := range ep.PrefixBlocks {
				if want == have {
					hits++
					break
				}
			}
		}
		if len(m.BlockKeys) > 0 {
			score += float64(hits) / float64(len(m.BlockKeys))
		}
		if score > best.Score {
			best = Decision{Endpoint: ep.Name, Score: score}
		}
	}
	return best
}

// ParseBodyThenDecide is the status-quo path: the plugin receives raw bytes and
// does its own parse before it can decide. Arm C uses this.
func (s *scorer) ParseBodyThenDecide(body []byte) (Decision, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return Decision{}, err
	}
	m := MetadataFromRequest(&req)
	return s.Decide(m), nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
