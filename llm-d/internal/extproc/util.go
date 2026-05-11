package extproc

import (
	"os"
	"strings"
)

// SplitCSV splits a comma-separated env/flag value, trimming whitespace.
// Empty input returns nil (so len() == 0 works as a presence check).
func SplitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// EnvOr returns os.Getenv(key) when set, otherwise fallback.
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
