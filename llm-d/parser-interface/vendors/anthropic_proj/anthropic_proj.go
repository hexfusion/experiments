// Package anthropic_proj is a projection-based variant of vendors/anthropic.
// Same wire format, same InferenceInput contract; Parse retains raw bytes
// and accessors / event handlers project fields via gjson.
package anthropic_proj

import (
	"bytes"
	"context"

	"github.com/tidwall/gjson"

	"prototype/kv"
	"prototype/parser"
)

var Vendor = parser.Vendor{
	Name:   "anthropic_proj",
	Parser: parserImpl{},
	Decoder: parser.NewDecoder(map[string]parser.EventHandler{
		"message_start": messageStart,
		"message_delta": messageDelta,
		"message_stop":  messageStop,
	}),
}

type parserImpl struct{}

func (parserImpl) Parse(_ context.Context, raw []byte) (parser.InferenceInput, error) {
	if !gjson.ValidBytes(raw) {
		return nil, errInvalid
	}
	return &request{raw: raw, BaseAttrs: kv.NewBaseAttrs()}, nil
}

// Accessors memoize on first call. Projection's gjson walks are expensive
// enough (~tens to hundreds of ns per call) to make caching worth a single
// branch + cached value per accessor. Single-writer-per-request, so no
// atomics needed.
type request struct {
	raw []byte
	kv.BaseAttrs

	modelDone, streamDone, promptDone, blocksDone bool
	modelVal                                      string
	streamVal                                     bool
	promptVal                                     []byte
	blocksVal                                     []parser.Block
}

func (r *request) Model() string {
	if !r.modelDone {
		r.modelVal = gjson.GetBytes(r.raw, "model").String()
		r.modelDone = true
	}
	return r.modelVal
}

func (r *request) Stream() bool {
	if !r.streamDone {
		r.streamVal = gjson.GetBytes(r.raw, "stream").Bool()
		r.streamDone = true
	}
	return r.streamVal
}

func (r *request) Prompt() []byte {
	if r.promptDone {
		return r.promptVal
	}
	var b bytes.Buffer
	gjson.GetBytes(r.raw, "messages").ForEach(func(_, msg gjson.Result) bool {
		b.WriteString(msg.Get("role").String())
		b.WriteString(": ")
		b.WriteString(msg.Get("content").String())
		b.WriteByte('\n')
		return true
	})
	r.promptVal = b.Bytes()
	r.promptDone = true
	return r.promptVal
}

func (r *request) PromptBlocks() []parser.Block {
	if r.blocksDone {
		return r.blocksVal
	}
	var blocks []parser.Block
	gjson.GetBytes(r.raw, "messages").ForEach(func(_, msg gjson.Result) bool {
		blocks = append(blocks, parser.Block{
			Kind: parser.BlockText,
			Text: msg.Get("role").String() + ": " + msg.Get("content").String(),
		})
		return true
	})
	r.blocksVal = blocks
	r.blocksDone = true
	return r.blocksVal
}

func messageStart(_ context.Context, data []byte, in parser.InferenceInput) error {
	tokens := gjson.GetBytes(data, "message.usage.input_tokens").Int()
	kv.Update(in, kv.UsageKey, &kv.Usage{Prompt: int(tokens)})
	return nil
}

func messageDelta(_ context.Context, data []byte, in parser.InferenceInput) error {
	out := gjson.GetBytes(data, "usage.output_tokens").Int()
	u, _ := kv.Get(in, kv.UsageKey)
	if u == nil {
		u = &kv.Usage{}
	}
	u.Completion = int(out)
	kv.Update(in, kv.UsageKey, u)
	if stop := gjson.GetBytes(data, "delta.stop_reason").String(); stop != "" {
		kv.Update(in, kv.FinishReasonKey, stop)
	}
	return nil
}

func messageStop(_ context.Context, _ []byte, _ parser.InferenceInput) error {
	return nil
}

type sentinelErr string

func (e sentinelErr) Error() string { return string(e) }

const errInvalid sentinelErr = "anthropic_proj: invalid JSON"
