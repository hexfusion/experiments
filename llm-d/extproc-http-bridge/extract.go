package main

import (
	"context"
	"encoding/json"
)

// Extractor is the only component that sees request bytes. Everything
// downstream works from Metadata.
type Extractor interface {
	Extract(ctx context.Context, body []byte, m *Metadata) error
}

// OpenAIExtractor pulls routing fields out of a chat-completions body. It is
// deliberately small: the point of the contract is that adding a field here is
// the only way to give plugins more, so nobody is tempted to ship them bytes.
type OpenAIExtractor struct{}

func (OpenAIExtractor) Extract(_ context.Context, body []byte, m *Metadata) error {
	if len(body) == 0 {
		return nil
	}
	var req struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	m.Model = req.Model
	m.Stream = req.Stream
	n := 0
	for _, msg := range req.Messages {
		n += len(msg.Content)
	}
	// Cheap stand-in for a token count. A real deployment calls the render
	// endpoint here, once, and publishes the result to every plugin.
	m.TokenCount = n / 4
	return nil
}
