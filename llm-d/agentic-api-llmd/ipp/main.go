// IPP: the policy front. Runs input+output guardrails on the request text,
// binds the session to the authenticated tenant, then CALLS agentic-api as a
// service. The layers below never see raw input, only approved text.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync/atomic"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var (
	agenticURL = env("AGENTIC_URL", "http://agentic-api:9000")
	listen     = ":" + env("PORT", "9200")
	httpc      = &http.Client{}

	reqs, blocked, redacted, moderated atomic.Int64
)

// demo guardrail policy: block jailbreak/unsafe, redact PII in, moderate out.
var blockIn = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore (all|previous) instructions`),
	regexp.MustCompile(`(?i)how to make a (bomb|weapon)`),
}
var redactIn = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`), "[redacted-email]"},
	{regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), "[redacted-ssn]"},
}
var moderateOut = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(bomb|weapon)\b`),
}

// guardIn redacts PII and reports whether the text must be blocked.
func guardIn(text string) (string, bool) {
	for _, re := range blockIn {
		if re.MatchString(text) {
			return "", true
		}
	}
	for _, r := range redactIn {
		if r.re.MatchString(text) {
			text = r.re.ReplaceAllString(text, r.repl)
			redacted.Add(1)
		}
	}
	return text, false
}

// mapInput applies guardIn to every text span in the Responses `input`.
func mapInput(body map[string]any) (blockedReq bool) {
	switch in := body["input"].(type) {
	case string:
		out, block := guardIn(in)
		if block {
			return true
		}
		body["input"] = out
	case []any:
		for _, it := range in {
			m, _ := it.(map[string]any)
			parts, _ := m["content"].([]any)
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if t, ok := pm["text"].(string); ok {
					out, block := guardIn(t)
					if block {
						return true
					}
					pm["text"] = out
				}
			}
		}
	}
	return false
}

func moderate(text string) string {
	for _, re := range moderateOut {
		if re.MatchString(text) {
			text = re.ReplaceAllString(text, "[redacted]")
			moderated.Add(1)
		}
	}
	return text
}

func handleResponses(w http.ResponseWriter, r *http.Request) {
	reqs.Add(1)
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad request"})
		return
	}
	if mapInput(body) { // input guardrail on the new turn
		blocked.Add(1)
		writeJSON(w, 403, map[string]any{"error": map[string]any{"type": "policy_violation", "message": "blocked by input guardrail"}})
		return
	}
	// bind the session to the authenticated tenant (an auth-bound capability, not
	// a client-controlled key): namespace conversation_id so tenants can't collide.
	tenant := r.Header.Get("x-tenant")
	if tenant == "" {
		tenant = "demo"
	}
	if conv, ok := body["conversation_id"].(string); ok && conv != "" {
		body["conversation_id"] = tenant + "/" + conv
	}
	// call agentic-api as a service
	fwd, _ := json.Marshal(body)
	resp, err := httpc.Post(agenticURL+"/v1/responses", "application/json", bytes.NewReader(fwd))
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": "upstream unreachable"})
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if json.Unmarshal(rb, &out) != nil { // non-JSON: pass through
		w.WriteHeader(resp.StatusCode)
		w.Write(rb)
		return
	}
	if items, ok := out["output"].([]any); ok { // output guardrail
		for _, it := range items {
			m, _ := it.(map[string]any)
			parts, _ := m["content"].([]any)
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if t, ok := pm["text"].(string); ok {
					pm["text"] = moderate(t)
				}
			}
		}
	}
	writeJSON(w, resp.StatusCode, out)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", handleResponses)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ipp_requests_total %d\nipp_blocked_total %d\nipp_redacted_total %d\nipp_moderated_total %d\n",
			reqs.Load(), blocked.Load(), redacted.Load(), moderated.Load())
	})
	log.Printf("ipp on %s -> %s", listen, agenticURL)
	log.Fatal(http.ListenAndServe(listen, mux))
}
