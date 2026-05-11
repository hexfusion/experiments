package extproc

import (
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

// HeaderValue returns the first value of key from an Envoy HeaderMap,
// preferring Value over RawValue (Envoy ext_proc puts strings in either).
// Match is case-insensitive.
func HeaderValue(h *corev3.HeaderMap, key string) string {
	if h == nil {
		return ""
	}
	for _, hdr := range h.Headers {
		if strings.EqualFold(hdr.Key, key) {
			if hdr.Value != "" {
				return hdr.Value
			}
			return string(hdr.RawValue)
		}
	}
	return ""
}
