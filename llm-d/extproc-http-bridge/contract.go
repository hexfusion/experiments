package main

// The plugin contract. A plugin is a plain HTTP handler: it receives extracted
// metadata and returns a decision. It never sees the request body, never speaks
// gRPC, and never deals with ext_proc processing modes.

// Metadata is what the bridge extracts once and hands to every plugin.
type Metadata struct {
	RequestID string            `json:"request_id"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers"`

	// Body-derived fields. Populated by the extractor, which is the only
	// component that sees bytes.
	Model      string `json:"model,omitempty"`
	Stream     bool   `json:"stream,omitempty"`
	BodyBytes  int    `json:"body_bytes,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`

	// Attributes carries anything a prior plugin published for later ones.
	// Keys are namespaced by producer so two plugins cannot collide.
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Decision is what a plugin returns. Everything a plugin can do to a request is
// expressible here, which is what keeps ext_proc's semantics out of plugin code.
type Decision struct {
	// SetHeaders are added or overwritten on the request before it is forwarded.
	SetHeaders map[string]string `json:"set_headers,omitempty"`
	// RemoveHeaders are dropped from the request.
	RemoveHeaders []string `json:"remove_headers,omitempty"`
	// Publish adds attributes visible to later plugins in the chain.
	Publish map[string]any `json:"publish,omitempty"`
	// Immediate short-circuits: the gateway replies without contacting the
	// backend. This is how auth denial and rate limiting are expressed.
	Immediate *ImmediateResponse `json:"immediate,omitempty"`
}

// ImmediateResponse ends the request at the gateway.
type ImmediateResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// ResponseMetadata is the response-phase equivalent, delivered once the
// response completes rather than per chunk.
type ResponseMetadata struct {
	RequestID  string            `json:"request_id"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
	Streamed   bool              `json:"streamed"`
	Events     int               `json:"events,omitempty"`
	Usage      *Usage            `json:"usage,omitempty"`
	// Complete is false when the client disconnected or the stream aborted, so
	// a consumer can tell a real zero from a missing measurement.
	Complete bool `json:"complete"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// PluginSpec declares a plugin and, critically, what it needs. Declaring needs
// at registration is what lets the bridge decide the ext_proc processing mode
// and skip work nobody asked for.
type PluginSpec struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// NeedsBody is false for plugins that decide from metadata alone, which
	// should be all of them. When every plugin in the chain sets this false the
	// bridge can run in a mode that never returns the body to the gateway.
	NeedsBody bool `json:"needs_body"`
	// MutatesRequest declares intent to change the request. The bridge selects
	// a processing mode accordingly.
	MutatesRequest bool `json:"mutates_request"`
	// WantsResponse subscribes to the response phase.
	WantsResponse bool `json:"wants_response"`
}
