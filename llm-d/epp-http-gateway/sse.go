package main

import (
	"bytes"
	"encoding/json"
)

// SSEDecoder folds streamed response chunks into usage. It is stateful across
// chunks deliberately: splitting per transport chunk drops any event that
// straddles a boundary, which is a live defect in the current implementation
// and the reason streamed usage goes missing there.
type SSEDecoder struct {
	buf    bytes.Buffer
	usage  *Usage
	events int
	done   bool
}

func (d *SSEDecoder) Decode(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	d.buf.Write(chunk)
	for {
		raw := d.buf.Bytes()
		i := bytes.Index(raw, []byte("\n\n"))
		if i < 0 {
			return nil // incomplete event, keep it buffered
		}
		event := make([]byte, i)
		copy(event, raw[:i])
		d.buf.Next(i + 2)

		line := bytes.TrimPrefix(bytes.TrimSpace(event), []byte("data: "))
		if len(line) == 0 {
			continue
		}
		if bytes.Equal(line, []byte("[DONE]")) {
			d.done = true
			continue
		}
		d.events++
		var probe struct {
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Usage != nil {
			d.usage = &Usage{
				PromptTokens:     probe.Usage.PromptTokens,
				CompletionTokens: probe.Usage.CompletionTokens,
				TotalTokens:      probe.Usage.TotalTokens,
			}
		}
	}
}

// Usage returns what was extracted. A nil usage chunk yields zeroes with the
// event count intact, which is the honest answer when the client never set
// stream_options.include_usage and the final chunk therefore never existed.
func (d *SSEDecoder) Usage() Usage {
	u := Usage{Events: d.events}
	if d.usage != nil {
		u.PromptTokens = d.usage.PromptTokens
		u.CompletionTokens = d.usage.CompletionTokens
		u.TotalTokens = d.usage.TotalTokens
	}
	return u
}
