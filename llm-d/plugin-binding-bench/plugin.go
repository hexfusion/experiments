package main

import (
	"context"
	"encoding/json"
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
	Name       string
	QueueDepth int
	KVUtil     float64
}

// Indexer stands in for the KV-cache block indexer: block hash to the endpoints
// known to hold that block.
type Indexer struct {
	blocks map[uint64][]int
}

func NewIndexer() *Indexer { return &Indexer{blocks: map[uint64][]int{}} }

// Warm marks the first depth blocks of keys as cached on serversPerBlock
// endpoints. Depth is what decides producer cost, because the match breaks at
// the first miss.
func (ix *Indexer) Warm(keys []uint64, depth, serversPerBlock, endpoints int) {
	if depth > len(keys) {
		depth = len(keys)
	}
	for i := 0; i < depth; i++ {
		servers := make([]int, 0, serversPerBlock)
		for s := 0; s < serversPerBlock && s < endpoints; s++ {
			servers = append(servers, (i+s)%endpoints)
		}
		ix.blocks[keys[i]] = servers
	}
}

func (ix *Indexer) Get(h uint64) []int { return ix.blocks[h] }

// PrefixMatch is the producer's output: matched block count per endpoint. This
// is the shape llm-d's PrefixCacheMatchInfo has, and the Scorer consumes it
// rather than computing it.
type PrefixMatch struct {
	Matched map[int]int
	Total   int
}

// MatchLongestPrefix mirrors the real data producer: greedy from the longest
// prefix, breaking on the first block no endpoint holds. Cost is therefore
// bounded by matched depth, not by prompt length, and varies by orders of
// magnitude with cache state.
func (ix *Indexer) MatchLongestPrefix(keys []uint64) PrefixMatch {
	res := make(map[int]int)
	for _, h := range keys {
		servers := ix.Get(h)
		if len(servers) == 0 {
			break
		}
		for _, s := range servers {
			res[s]++
		}
	}
	return PrefixMatch{Matched: res, Total: len(keys)}
}

// scorer is an EPP-shaped Scorer: O(endpoints), one lookup and a few float ops
// each, consuming precomputed match info. The expensive prefix work lives in
// the producer above, which under this design belongs to the extraction stage.
type scorer struct {
	name      string
	endpoints []Endpoint
}

func NewScorer(name string, endpoints int) Plugin {
	eps := make([]Endpoint, endpoints)
	for i := range eps {
		eps[i] = Endpoint{
			Name:       "pod-" + itoa(i),
			QueueDepth: i % 17,
			KVUtil:     float64(i%100) / 100.0,
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
		if m.Prefix.Total > 0 {
			score += float64(m.Prefix.Matched[i]) / float64(m.Prefix.Total)
		}
		if score > best.Score {
			best = Decision{Endpoint: ep.Name, Score: score}
		}
	}
	return best
}

// ParseBodyThenDecide is the status-quo path: the consumer got raw bytes, so it
// must parse and run the producer itself before it can score.
func (s *scorer) ParseBodyThenDecide(body []byte) (Decision, error) {
	m, err := globalExtractor.Extract(body)
	if err != nil {
		return Decision{}, err
	}
	return s.Decide(m), nil
}

// Extractor is the shim's stage: parse the body and run the data producer once.
// In the status-quo arms every consumer runs this for itself.
type Extractor struct {
	indexer  *Indexer
	render   *RenderClient
	sessions *SessionCache
}

func NewExtractor(ix *Indexer) *Extractor {
	return &Extractor{indexer: ix, sessions: NewSessionCache()}
}

// WithRender routes tokenization through the render service instead of doing it
// locally, which is what real EPP does.
func (e *Extractor) WithRender(rc *RenderClient) *Extractor {
	e.render = rc
	return e
}

func (e *Extractor) Extract(body []byte) (*Metadata, error) {
	return e.ExtractCtx(context.Background(), body)
}

func (e *Extractor) ExtractCtx(ctx context.Context, body []byte) (*Metadata, error) {
	if e.render != nil {
		// The whole body goes to render and token ids come back. The local
		// parse still happens because routing needs model and stream.
		rr, err := e.render.Render(ctx, body)
		if err != nil {
			return nil, err
		}
		var chat ChatRequest
		if err := json.Unmarshal(body, &chat); err != nil {
			return nil, err
		}
		m := &Metadata{
			Model: chat.Model, Stream: chat.Stream,
			TokenCount: rr.TokenCount, Tokens: rr.Tokens, BlockKeys: rr.BlockKeys,
		}
		if e.indexer != nil {
			m.Prefix = e.indexer.MatchLongestPrefix(m.BlockKeys)
		}
		return m, nil
	}
	m, err := parseBody(body)
	if err != nil {
		return nil, err
	}
	if e.indexer != nil {
		m.Prefix = e.indexer.MatchLongestPrefix(m.BlockKeys)
	}
	return m, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
