package httpshim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/llm-d/llm-d-router/pkg/epp/datalayer"
	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwkrh "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/requesthandling/parsers/openai"
	"github.com/llm-d/llm-d-router/pkg/epp/handlers"
	"github.com/llm-d/llm-d-router/pkg/epp/handlers/httpshim"
	"github.com/llm-d/llm-d-router/pkg/epp/metadata"
)

const wantEndpoint = "10.244.0.11:8000"

type fakeDatastore struct{}

func (fakeDatastore) PoolGet() (*datalayer.EndpointPool, error) {
	return &datalayer.EndpointPool{}, nil
}

// fakeDirector stands in for scheduling. Everything upstream of it, the phase
// handling and the response construction, is EPP's real code.
type fakeDirector struct{ calls int64 }

func (d *fakeDirector) HandleRequest(_ context.Context, reqCtx *handlers.RequestContext, _ *fwkrh.InferenceRequestBody) (*handlers.RequestContext, error) {
	reqCtx.TargetEndpoint = wantEndpoint
	return reqCtx, nil
}
func (d *fakeDirector) HandleResponseHeader(_ context.Context, r *handlers.RequestContext) *handlers.RequestContext {
	return r
}
func (d *fakeDirector) HandleResponseBody(_ context.Context, r *handlers.RequestContext, _ bool) *handlers.RequestContext {
	return r
}
func (d *fakeDirector) GetRandomEndpoint() *fwkdl.EndpointMetadata { return nil }

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	reg := handlers.NewParserRegistry([]fwkrh.Parser{openai.NewOpenAIParser()}, logr.Discard())
	proc := handlers.NewStreamingServer(fakeDatastore{}, &fakeDirector{}, reg, 1<<20)

	srv := httptest.NewServer(&httpshim.Handler{Proc: proc})
	t.Cleanup(srv.Close)
	return srv
}

func body(i int) []byte {
	b, _ := json.Marshal(map[string]any{
		"model": "llama-3.1-8b",
		"messages": []map[string]string{
			{"role": "user", "content": fmt.Sprintf("request %d", i)},
		},
	})
	return b
}

func post(t *testing.T, url string, i int) httpshim.Decision {
	t.Helper()
	req, err := http.NewRequest("POST", url+"/v1/chat/completions", bytes.NewReader(body(i)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}

	var dec httpshim.Decision
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dec))
	return dec
}

// TestShimDrivesRealProcess is the claim: an HTTP request reaches a routing
// decision through EPP's unmodified ext_proc entry point.
func TestShimDrivesRealProcess(t *testing.T) {
	srv := newServer(t)
	dec := post(t, srv.URL, 0)

	require.Equal(t, wantEndpoint, dec.Endpoint)
	require.Equal(t, wantEndpoint, dec.Headers[metadata.DestinationEndpointKey],
		"the decision should arrive through EPP's own header mutation, not the shim")
}

// TestConcurrentRequests is the part that has to hold at rate. Each request runs
// its own Process on the shared StreamingServer, so the stream state must not be
// shared and no goroutine may outlive its request.
func TestConcurrentRequests(t *testing.T) {
	srv := newServer(t)

	// Warm one request so pools and lazy state are allocated before counting.
	post(t, srv.URL, 0)
	settle()
	before := runtime.NumGoroutine()

	const n = 300
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if got := post(t, srv.URL, i); got.Endpoint != wantEndpoint {
				errs <- fmt.Errorf("request %d got %q", i, got.Endpoint)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	settle()
	after := runtime.NumGoroutine()
	// Idle HTTP server goroutines linger, so allow slack. A per-request leak
	// would show up as roughly n, not a handful.
	require.Less(t, after-before, 50,
		"goroutines grew by %d over %d requests: Process is not returning", after-before, n)
}

func settle() {
	for i := 0; i < 10; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPathIsPassedThrough proves the shim owns no route of its own. EPP resolves
// parsers by path suffix, so an unknown path must be rejected by EPP naming that
// exact path, not swallowed or rewritten by the shim.
func TestPathIsPassedThrough(t *testing.T) {
	srv := newServer(t)

	req, err := http.NewRequest("POST", srv.URL+"/v1/not-a-real-api", bytes.NewReader(body(0)))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, string(raw), "/v1/not-a-real-api",
		"EPP should see the caller's path verbatim")
}
