// A plugin, in full. It is an HTTP handler that reads metadata and returns a
// decision. No gRPC, no ext_proc, no processing modes, no body.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
)

type metadata struct {
	RequestID  string            `json:"request_id"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Model      string            `json:"model"`
	TokenCount int               `json:"token_count"`
	BodyBytes  int               `json:"body_bytes"`
}

type immediate struct {
	Status int    `json:"status"`
	Body   string `json:"body,omitempty"`
}

type decision struct {
	SetHeaders map[string]string `json:"set_headers,omitempty"`
	Publish    map[string]any    `json:"publish,omitempty"`
	Immediate  *immediate        `json:"immediate,omitempty"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	limit := flag.Int("max-tokens", 0, "reject requests above this token count, 0 disables")
	flag.Parse()

	http.HandleFunc("/decide", func(w http.ResponseWriter, r *http.Request) {
		var m metadata
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		d := decision{}
		if *limit > 0 && m.TokenCount > *limit {
			d.Immediate = &immediate{
				Status: http.StatusTooManyRequests,
				Body:   fmt.Sprintf("prompt of %d tokens exceeds limit %d", m.TokenCount, *limit),
			}
		} else {
			// Routing decision from metadata alone.
			d.SetHeaders = map[string]string{"x-gateway-destination-endpoint": pick(m.Model)}
			d.Publish = map[string]any{"tokens": m.TokenCount}
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(d)
	})

	http.HandleFunc("/response", func(w http.ResponseWriter, r *http.Request) {
		var rm map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rm)
		fmt.Fprintf(os.Stderr, "response metadata: %v\n", rm)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	fmt.Printf("plugin on %s\n", *addr)
	_ = http.ListenAndServe(*addr, nil)
}

func pick(model string) string {
	if model == "" {
		return "10.0.0.1:8000"
	}
	// Stand-in for a scheduling decision.
	return fmt.Sprintf("10.0.0.%d:8000", 1+len(model)%4)
}
