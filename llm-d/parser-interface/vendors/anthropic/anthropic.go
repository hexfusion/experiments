package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"prototype/kv"
	"prototype/parser"
)

var Vendor = parser.Vendor{
	Name:   "anthropic",
	Parser: parserImpl{},
	Decoder: parser.NewDecoder(map[string]parser.EventHandler{
		"message_start": parser.DecodeJSON(messageStart),
		"message_delta": parser.DecodeJSON(messageDelta),
		"message_stop":  parser.DecodeJSON(messageStop),
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
		return nil, fmt.Errorf("anthropic: %w", err)
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

// PromptBlocks satisfies parser.PromptStructured. Anthropic content can be a
// string or an array of typed blocks; this prototype models the string case
// and translates each message into one BlockText.
func (r *request) PromptBlocks() []parser.Block {
	blocks := make([]parser.Block, 0, len(r.body.Messages))
	for _, m := range r.body.Messages {
		blocks = append(blocks, parser.Block{Kind: parser.BlockText, Text: m.Role + ": " + m.Content})
	}
	return blocks
}

type messageStartEvent struct {
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type messageDeltaEvent struct {
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
}

type messageStopEvent struct{}

func messageStart(_ context.Context, v messageStartEvent, in parser.InferenceInput) error {
	kv.Update(in, kv.UsageKey, &kv.Usage{Prompt: v.Message.Usage.InputTokens})
	return nil
}

func messageDelta(_ context.Context, v messageDeltaEvent, in parser.InferenceInput) error {
	u, _ := kv.Get(in, kv.UsageKey)
	if u == nil {
		u = &kv.Usage{}
	}
	u.Completion = v.Usage.OutputTokens
	kv.Update(in, kv.UsageKey, u)
	if v.Delta.StopReason != "" {
		kv.Update(in, kv.FinishReasonKey, v.Delta.StopReason)
	}
	return nil
}

func messageStop(_ context.Context, _ messageStopEvent, _ parser.InferenceInput) error {
	return nil
}
