package main

import (
	"context"
	"encoding/json"
	"sync"
)

// SessionState is what a session-affine owner of the bytes can carry between
// turns. Multi-turn chat resends the whole history every turn, so turn N+1 is
// turn N plus a new exchange, and everything already extracted stays valid.
type SessionState struct {
	Messages  int
	Tokens    []uint32
	BlockKeys []uint64
}

type SessionCache struct {
	mu sync.Mutex
	m  map[string]*SessionState
}

func NewSessionCache() *SessionCache { return &SessionCache{m: map[string]*SessionState{}} }

func (c *SessionCache) Get(id string) (*SessionState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.m[id]
	return s, ok
}

func (c *SessionCache) Put(id string, s *SessionState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = s
}

const blockSizeTokens = 16

// ExtractSession is delta extraction. When the session is known and the request
// extends what was already seen, only the new messages go to render; the token
// sequence is appended and block keys are recomputed from the last complete
// block boundary, because appending changes the trailing partial block.
//
// deltaUsed reports whether the fast path was taken, so callers can tell a
// genuine amortization from a silent full re-extract.
func (e *Extractor) ExtractSession(ctx context.Context, sessionID string, body []byte) (m *Metadata, deltaUsed bool, err error) {
	var chat ChatRequest
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, false, err
	}

	prev, ok := e.sessions.Get(sessionID)
	if !ok || prev.Messages == 0 || prev.Messages > len(chat.Messages) {
		m, err := e.extractFull(ctx, &chat, body)
		if err != nil {
			return nil, false, err
		}
		e.sessions.Put(sessionID, &SessionState{
			Messages: len(chat.Messages), Tokens: m.Tokens, BlockKeys: m.BlockKeys,
		})
		return m, false, nil
	}

	// Render only the messages this turn added.
	newMsgs := chat.Messages[prev.Messages:]
	if len(newMsgs) == 0 {
		m := e.assemble(&chat, prev.Tokens, prev.BlockKeys)
		return m, true, nil
	}

	deltaTokens, err := e.tokenize(ctx, &ChatRequest{Model: chat.Model, Messages: newMsgs})
	if err != nil {
		return nil, false, err
	}

	tokens := make([]uint32, 0, len(prev.Tokens)+len(deltaTokens))
	tokens = append(tokens, prev.Tokens...)
	tokens = append(tokens, deltaTokens...)

	// Block keys are valid up to the last complete block; recompute from there.
	keep := len(prev.Tokens) / blockSizeTokens
	if keep > len(prev.BlockKeys) {
		keep = len(prev.BlockKeys)
	}
	keys := make([]uint64, 0, len(tokens)/blockSizeTokens)
	keys = append(keys, prev.BlockKeys[:keep]...)
	keys = append(keys, blockKeysFrom(tokens, keep*blockSizeTokens)...)

	e.sessions.Put(sessionID, &SessionState{
		Messages: len(chat.Messages), Tokens: tokens, BlockKeys: keys,
	})
	return e.assemble(&chat, tokens, keys), true, nil
}

func (e *Extractor) assemble(chat *ChatRequest, tokens []uint32, keys []uint64) *Metadata {
	m := &Metadata{
		Model: chat.Model, Stream: chat.Stream,
		TokenCount: len(tokens), Tokens: tokens, BlockKeys: keys,
	}
	if e.indexer != nil {
		m.Prefix = e.indexer.MatchLongestPrefix(m.BlockKeys)
	}
	return m
}

func (e *Extractor) extractFull(ctx context.Context, chat *ChatRequest, body []byte) (*Metadata, error) {
	tokens, err := e.tokenize(ctx, chat)
	if err != nil {
		return nil, err
	}
	return e.assemble(chat, tokens, blockKeysFrom(tokens, 0)), nil
}

// tokenize goes through the render service when one is configured, which is
// where the amortization actually lands.
func (e *Extractor) tokenize(ctx context.Context, chat *ChatRequest) ([]uint32, error) {
	if e.render == nil {
		return metadataFromRequest(chat).Tokens, nil
	}
	b, err := json.Marshal(chat)
	if err != nil {
		return nil, err
	}
	rr, err := e.render.Render(ctx, b)
	if err != nil {
		return nil, err
	}
	return rr.Tokens, nil
}
