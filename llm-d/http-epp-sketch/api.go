package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// The decider's HTTP surface. Nothing here names a phase, a processing mode, or
// a stream. One request, one decision. That is the test: if the shape reveals
// ext_proc, it is emulation rather than an API.

// ScheduleRequest is what a caller sends to ask where a request should go.
type ScheduleRequest struct {
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	SessionID string `json:"session_id,omitempty"`

	// Body is the raw request. Attributes is the read-once alternative: a caller
	// that already parsed sends derived data and omits Body. Composition point
	// with READ-ONCE, not a second protocol.
	Body       json.RawMessage `json:"body,omitempty"`
	Attributes *Attributes     `json:"attributes,omitempty"`
}

// Attributes is pre-extracted routing data, so the body need not travel twice.
type Attributes struct {
	PromptTokens int    `json:"prompt_tokens"`
	PrefixHash   uint64 `json:"prefix_hash"`
}

// Header names for the same data carried as headers with no body at all.
//
// This is the carrier that works in both topologies. When IPP hands the request
// back to the gateway the body is the user's request continuing upstream, so
// headers are the only option; using them for the direct call too means one
// encoding rather than two. Everything here is a flat scalar, which is what the
// routing data actually is.
const (
	HdrRequestID    = "x-llmd-request-id"
	HdrModel        = "x-llmd-model"
	HdrPromptTokens = "x-llmd-prompt-tokens"
	HdrPrefixHash   = "x-llmd-prefix-hash"
	HdrSessionID    = "x-llmd-session-id"
)

// Model name and session id originate in the user's request body and end up in a
// header, so an unvalidated value is a header-injection primitive. Go's net/http
// happens to reject bad field values on write, but the guarantee must not depend
// on which implementation in the chain stamps the header.
//
// Validation rather than base64: encoding hides the problem without fixing it,
// costs 33% expansion, and defeats HPACK's Huffman coding by flattening the byte
// distribution. A validly encoded garbage model name is still garbage.
const (
	maxModelLen  = 253
	maxOpaqueLen = 128
)

var ErrInvalidHeader = errors.New("invalid routing header")

// validToken rejects anything outside a conservative printable set. CR and LF
// are the injection vector; refusing everything else is defence in depth.
func validToken(s string, max int, extra string) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte(extra, c) >= 0:
		default:
			return false
		}
	}
	return true
}

// ValidModel admits the shape real model names take. Production should also
// match against the configured set rather than a charset alone.
func ValidModel(s string) bool { return validToken(s, maxModelLen, "-_./:") }

// ValidOpaque covers identifiers the caller mints or passes through.
func ValidOpaque(s string) bool { return validToken(s, maxOpaqueLen, "-_") }

// EncodeAttributes writes routing data onto a header set, refusing values that
// have no business in one. Rejecting at the boundary is the point: nothing
// downstream should have to wonder whether the header was checked.
func EncodeAttributes(h http.Header, reqID, model string, a *Attributes) error {
	if !ValidOpaque(reqID) {
		return fmt.Errorf("%w: request id", ErrInvalidHeader)
	}
	if !ValidModel(model) {
		return fmt.Errorf("%w: model", ErrInvalidHeader)
	}
	h.Set(HdrRequestID, reqID)
	h.Set(HdrModel, model)
	if a == nil {
		return nil
	}
	h.Set(HdrPromptTokens, strconv.Itoa(a.PromptTokens))
	h.Set(HdrPrefixHash, strconv.FormatUint(a.PrefixHash, 16))
	return nil
}

// DecodeAttributes reads it back and validates again, because the decider does
// not trust its caller either.
//
// Malformed attributes are not an error: routing degrades to load-only rather
// than failing, which is what lets a caller that does not publish metadata keep
// working. A malformed identity is an error, because there is nothing to
// degrade to.
func DecodeAttributes(h http.Header) (reqID, model string, a *Attributes, err error) {
	reqID, model = h.Get(HdrRequestID), h.Get(HdrModel)
	if !ValidOpaque(reqID) {
		return "", "", nil, fmt.Errorf("%w: request id", ErrInvalidHeader)
	}
	if !ValidModel(model) {
		return "", "", nil, fmt.Errorf("%w: model", ErrInvalidHeader)
	}
	tok, err1 := strconv.Atoi(h.Get(HdrPromptTokens))
	hash, err2 := strconv.ParseUint(h.Get(HdrPrefixHash), 16, 64)
	if err1 != nil || err2 != nil {
		return reqID, model, nil, nil
	}
	return reqID, model, &Attributes{PromptTokens: tok, PrefixHash: hash}, nil
}

// ScheduleResponse is the decision. Deny covers auth and rate-limit shapes, so
// policy stays expressible without the caller learning a protocol.
type ScheduleResponse struct {
	Endpoint    string            `json:"endpoint,omitempty"`
	TargetModel string            `json:"target_model,omitempty"`
	SetHeaders  map[string]string `json:"set_headers,omitempty"`
	Deny        *Denial           `json:"deny,omitempty"`
}

type Denial struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// Report closes the loop when a request finishes. Without it a decider's
// in-flight view only ever grows.
type Report struct {
	RequestID string `json:"request_id"`
	Endpoint  string `json:"endpoint"`
	Usage     Usage  `json:"usage"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
