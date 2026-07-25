package main

import (
	"encoding/json"
	"hash/maphash"
	"strings"
)

// ChatRequest is the shape the shim parses. Deliberately close to a real
// chat-completions body, since the parse cost is part of what is measured.
type ChatRequest struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Metadata is what a plugin decides from. Body bytes are not in it.
//
// Tokens is the honest part of this benchmark: the prefix-cache scorer in
// llm-d consumes the full token sequence, so metadata is not automatically
// small. Arms run with and without it.
type Metadata struct {
	Model      string   `json:"model"`
	Stream     bool     `json:"stream"`
	TokenCount int      `json:"token_count"`
	BlockKeys  []uint64 `json:"block_keys"`
	Tokens     []uint32 `json:"tokens,omitempty"`
	// Prefix is producer output, not parsed from the body. It travels to
	// plugins as metadata; the Scorer consumes it rather than computing it.
	Prefix PrefixMatch `json:"-"`
}

var metaSeed = maphash.MakeSeed()

// MetadataFromRequest is the parse-once extraction. Tokenization is modelled as
// whitespace splitting, which is the wrong algorithm but the right order of
// magnitude for how much per-token work a real tokenizer forces.
func metadataFromRequest(r *ChatRequest) *Metadata {
	m := &Metadata{Model: r.Model, Stream: r.Stream}
	const blockSize = 16
	var tokens []uint32
	for i := range r.Messages {
		for _, w := range strings.Fields(r.Messages[i].Content) {
			tokens = append(tokens, uint32(maphash.String(metaSeed, w)))
		}
	}
	m.Tokens = tokens
	m.TokenCount = len(tokens)
	for i := 0; i+blockSize <= len(tokens); i += blockSize {
		var b strings.Builder
		for _, t := range tokens[i : i+blockSize] {
			b.WriteByte(byte(t))
			b.WriteByte(byte(t >> 8))
		}
		m.BlockKeys = append(m.BlockKeys, maphash.String(metaSeed, b.String()))
	}
	return m
}

// Encode serialises metadata for a binding that crosses a process boundary.
// withTokens controls whether the full token sequence travels, which is the
// difference between small metadata and metadata the size of the prompt.
func (m *Metadata) Encode(withTokens bool) ([]byte, error) {
	if withTokens {
		return json.Marshal(m)
	}
	trimmed := *m
	trimmed.Tokens = nil
	return json.Marshal(&trimmed)
}

// parseBody unmarshals and extracts routing fields. Producer output is added
// separately by the Extractor.
func parseBody(body []byte) (*Metadata, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return metadataFromRequest(&req), nil
}

func DecodeMetadata(b []byte) (*Metadata, error) {
	m := &Metadata{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

// EncodeBinary is a compact fixed-width encoding. JSON turns every token into
// decimal text with a separator, which for a 50k-token prompt costs more than
// carrying the body. This separates "metadata is large" from "JSON is the wrong
// encoding for it".
func (m *Metadata) EncodeBinary() []byte {
	size := 4 + len(m.Model) + 1 + 4 + 4 + len(m.BlockKeys)*8 + 4 + len(m.Tokens)*4
	buf := make([]byte, 0, size)
	buf = appendU32(buf, uint32(len(m.Model)))
	buf = append(buf, m.Model...)
	if m.Stream {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	buf = appendU32(buf, uint32(m.TokenCount))
	buf = appendU32(buf, uint32(len(m.BlockKeys)))
	for _, k := range m.BlockKeys {
		buf = appendU64(buf, k)
	}
	buf = appendU32(buf, uint32(len(m.Tokens)))
	for _, t := range m.Tokens {
		buf = appendU32(buf, t)
	}
	return buf
}

// DecodeBinary reads the fixed-width form. Slices alias buf where the layout
// allows it, which is the point of a fixed-width encoding.
func DecodeBinary(buf []byte) (*Metadata, error) {
	m := &Metadata{}
	p := 0
	need := func(n int) error {
		if p+n > len(buf) {
			return errShortBuffer
		}
		return nil
	}
	if err := need(4); err != nil {
		return nil, err
	}
	nameLen := int(readU32(buf[p:]))
	p += 4
	if err := need(nameLen + 1 + 8); err != nil {
		return nil, err
	}
	m.Model = string(buf[p : p+nameLen])
	p += nameLen
	m.Stream = buf[p] == 1
	p++
	m.TokenCount = int(readU32(buf[p:]))
	p += 4
	nBlocks := int(readU32(buf[p:]))
	p += 4
	if err := need(nBlocks * 8); err != nil {
		return nil, err
	}
	m.BlockKeys = make([]uint64, nBlocks)
	for i := range m.BlockKeys {
		m.BlockKeys[i] = readU64(buf[p:])
		p += 8
	}
	if err := need(4); err != nil {
		return nil, err
	}
	nTokens := int(readU32(buf[p:]))
	p += 4
	if err := need(nTokens * 4); err != nil {
		return nil, err
	}
	m.Tokens = make([]uint32, nTokens)
	for i := range m.Tokens {
		m.Tokens[i] = readU32(buf[p:])
		p += 4
	}
	return m, nil
}

var errShortBuffer = errShort{}

type errShort struct{}

func (errShort) Error() string { return "short metadata buffer" }

func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func appendU64(b []byte, v uint64) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

func readU32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func readU64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

