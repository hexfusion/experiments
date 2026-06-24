package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"prototype/kv"
	"prototype/parser"
)

var Vendor = parser.Vendor{
	Name:   "openai",
	Parser: parserImpl{},
	Decoder: parser.NewDecoder(map[string]parser.EventHandler{
		"chunk": parser.DecodeJSON(chunk),
	}),
}

type body struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
	Messages []message `json:"messages"`
}

type message struct {
	Role, Content string
}

type parserImpl struct{}

func (parserImpl) Parse(_ context.Context, raw []byte) (parser.InferenceInput, error) {
	var b body
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	return &request{body: b, BaseAttrs: kv.NewBaseAttrs()}, nil
}

type request struct {
	body body
	kv.BaseAttrs
}

func (r *request) Model() string { return r.body.Model }
func (r *request) Stream() bool  { return r.body.Stream }

func (r *request) Prompt() []byte {
	var buf bytes.Buffer
	for _, m := range r.body.Messages {
		buf.WriteString(m.Role)
		buf.WriteString(": ")
		buf.WriteString(m.Content)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// PromptBlocks satisfies parser.PromptStructured. Chat content can be a
// string or an array of typed parts; this prototype models the string case
// and translates each message into one BlockText.
func (r *request) PromptBlocks() []parser.Block {
	blocks := make([]parser.Block, 0, len(r.body.Messages))
	for _, m := range r.body.Messages {
		blocks = append(blocks, parser.Block{Kind: parser.BlockText, Text: m.Role + ": " + m.Content})
	}
	return blocks
}

// Usage arrives only in the final chunk (and only when stream_options.include_usage is set).
type chunkEvent struct {
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func chunk(_ context.Context, v chunkEvent, in parser.InferenceInput) error {
	if v.Usage != nil {
		kv.Update(in, kv.UsageKey, &kv.Usage{
			Prompt:     v.Usage.PromptTokens,
			Completion: v.Usage.CompletionTokens,
		})
	}
	for _, c := range v.Choices {
		if c.FinishReason != "" {
			kv.Update(in, kv.FinishReasonKey, c.FinishReason)
		}
	}
	return nil
}
