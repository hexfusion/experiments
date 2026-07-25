package main

import (
	"bytes"
	"encoding/json"
)

// SSEDecoder folds streamed response chunks into usage metadata. It is stateful
// across chunks on purpose: splitting per transport chunk drops any event that
// straddles a boundary, which is a live defect in the current implementation.
type SSEDecoder struct {
	buf    bytes.Buffer
	Usage  *Usage
	Events int
	Done   bool
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
			return nil
		}
		event := make([]byte, i)
		copy(event, raw[:i])
		d.buf.Next(i + 2)

		line := bytes.TrimPrefix(bytes.TrimSpace(event), []byte("data: "))
		if len(line) == 0 {
			continue
		}
		if bytes.Equal(line, []byte("[DONE]")) {
			d.Done = true
			continue
		}
		d.Events++
		var probe struct {
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Usage != nil {
			d.Usage = probe.Usage
		}
	}
}
