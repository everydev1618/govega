package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeOpenAIEndpoint stands in for an OpenAI-compatible server (LM Studio,
// Ollama, vLLM): it advertises a fixed lineup on /v1/models and records the
// body of every chat request so a test can assert what actually went on the
// wire — the only thing that settles which model was used.
type fakeOpenAIEndpoint struct {
	served     []string
	modelsCode int

	mu   sync.Mutex
	last openaiRequest
	hits int
}

func (f *fakeOpenAIEndpoint) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			f.mu.Lock()
			f.hits++
			code := f.modelsCode
			f.mu.Unlock()
			if code != 0 && code != http.StatusOK {
				w.WriteHeader(code)
				return
			}
			data := make([]map[string]any, 0, len(f.served))
			for _, id := range f.served {
				data = append(data, map[string]any{"id": id, "object": "model"})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
			return
		}
		var got openaiRequest
		_ = json.NewDecoder(r.Body).Decode(&got)
		f.mu.Lock()
		f.last = got
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okOpenAIJSON))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeOpenAIEndpoint) lastRequest() openaiRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *fakeOpenAIEndpoint) modelsHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

// TestOpenAI_HonorsServedContextModel is the headline fix: a per-agent or
// per-step model override must reach the wire when the endpoint can serve it.
// Without this, an agent lineup that routes cheap steps to a small model
// silently ran every step on the one configured default.
func TestOpenAI_HonorsServedContextModel(t *testing.T) {
	fake := &fakeOpenAIEndpoint{served: []string{"big-27b", "small-9b"}}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	ctx := ContextWithOptions(context.Background(), Options{Model: "small-9b"})
	if _, err := o.Generate(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := fake.lastRequest().Model; got != "small-9b" {
		t.Errorf("wire model = %q, want the requested override %q", got, "small-9b")
	}
}

// TestOpenAI_UnservedModelFallsBackToDefault protects the self-hosting case:
// agent YAML written for Anthropic names models this endpoint has never heard
// of. Sending "claude-haiku-4-5-20251001" to LM Studio is a 404 and a dead
// turn, so an unservable override must degrade to the configured default
// rather than break the call.
func TestOpenAI_UnservedModelFallsBackToDefault(t *testing.T) {
	fake := &fakeOpenAIEndpoint{served: []string{"big-27b"}}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	ctx := ContextWithOptions(context.Background(), Options{Model: "claude-haiku-4-5-20251001"})
	if _, err := o.Generate(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := fake.lastRequest().Model; got != "big-27b" {
		t.Errorf("wire model = %q, want the default %q", got, "big-27b")
	}
}

// TestOpenAI_NoOverrideUsesConfiguredModel pins the default path.
func TestOpenAI_NoOverrideUsesConfiguredModel(t *testing.T) {
	fake := &fakeOpenAIEndpoint{served: []string{"big-27b"}}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	if _, err := o.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := fake.lastRequest().Model; got != "big-27b" {
		t.Errorf("wire model = %q, want %q", got, "big-27b")
	}
}

// TestOpenAI_UnreachableModelsEndpointStillGenerates: plenty of
// OpenAI-compatible proxies do not implement /v1/models. Failing to probe
// must cost us the override, never the turn.
func TestOpenAI_UnreachableModelsEndpointStillGenerates(t *testing.T) {
	fake := &fakeOpenAIEndpoint{modelsCode: http.StatusInternalServerError}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	ctx := ContextWithOptions(context.Background(), Options{Model: "small-9b"})
	if _, err := o.Generate(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := fake.lastRequest().Model; got != "big-27b" {
		t.Errorf("wire model = %q, want the default %q when the lineup is unknown", got, "big-27b")
	}
}

// TestOpenAI_ModelLineupIsCached keeps the probe off the hot path: one
// /v1/models call should serve many generations.
func TestOpenAI_ModelLineupIsCached(t *testing.T) {
	fake := &fakeOpenAIEndpoint{served: []string{"big-27b", "small-9b"}}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	ctx := ContextWithOptions(context.Background(), Options{Model: "small-9b"})
	for i := 0; i < 3; i++ {
		if _, err := o.Generate(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
			t.Fatalf("Generate %d: %v", i, err)
		}
	}
	if hits := fake.modelsHits(); hits != 1 {
		t.Errorf("/v1/models probed %d times, want 1 (cached)", hits)
	}
}

// TestOpenAI_HonorsMaxTokensAndTemperature: Model was not the only casualty
// of never reading the context — the same Options carry per-agent sampling
// settings, and buildRequest hardcoded MaxTokens at 8192.
func TestOpenAI_HonorsMaxTokensAndTemperature(t *testing.T) {
	fake := &fakeOpenAIEndpoint{served: []string{"big-27b"}}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	temp := 0.2
	ctx := ContextWithOptions(context.Background(), Options{MaxTokens: 512, Temperature: &temp})
	if _, err := o.Generate(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := fake.lastRequest()
	if got.MaxTokens != 512 {
		t.Errorf("MaxTokens = %d, want 512", got.MaxTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.2 {
		t.Errorf("Temperature = %v, want 0.2", got.Temperature)
	}
}

// TestOpenAI_ResponseCarriesWireModel: the caller logs and prices whatever
// the response names, so a fallback that stays silent in the response is a
// local call recorded as a hosted one.
func TestOpenAI_ResponseCarriesWireModel(t *testing.T) {
	fake := &fakeOpenAIEndpoint{served: []string{"big-27b"}}
	srv := fake.start(t)
	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAIModel("big-27b"))

	ctx := ContextWithOptions(context.Background(), Options{Model: "claude-haiku-4-5-20251001"})
	resp, err := o.Generate(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Model != "big-27b" {
		t.Errorf("resp.Model = %q, want the model that actually answered %q", resp.Model, "big-27b")
	}
}

// TestOpenAI_StreamClientCarriesNoOverallTimeout mirrors the rule anthropic.go
// already follows: http.Client.Timeout spans the entire response body, so a
// client-level timeout on the streaming path cuts long answers off mid-stream
// rather than failing fast. The OpenAI path used one client for both, which
// put a hard 5-minute ceiling on every self-hosted generation — reachable on a
// local model, where slow prefill on a long context plus a long answer is an
// ordinary afternoon, and the symptom is a truncated reply, not an error.
func TestOpenAI_StreamClientCarriesNoOverallTimeout(t *testing.T) {
	o := NewOpenAI(WithOpenAIBaseURL("http://127.0.0.1:1"))
	if o.streamClient == nil {
		t.Fatal("streamClient is nil; the streaming path must not share the bounded client")
	}
	if o.streamClient.Timeout != 0 {
		t.Errorf("streamClient.Timeout = %v, want 0 (unbounded; the caller's context cancels)",
			o.streamClient.Timeout)
	}
	if o.httpClient.Timeout == 0 {
		t.Error("httpClient.Timeout = 0; the non-streaming path should stay bounded")
	}
}

// TestOpenAI_StreamOutlivesTheSyncTimeout proves the split is wired, not just
// declared: a stream that takes longer than the sync bound still completes.
func TestOpenAI_StreamOutlivesTheSyncTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{
			`data: {"choices":[{"delta":{"content":"slow "}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":"but done"}}]}` + "\n\n",
			"data: [DONE]\n\n",
		} {
			time.Sleep(60 * time.Millisecond)
			_, _ = w.Write([]byte(chunk))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	o := NewOpenAI(WithOpenAIBaseURL(srv.URL), WithOpenAITimeout(50*time.Millisecond))

	ch, err := o.GenerateStream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	var text string
	for ev := range ch {
		if ev.Type == StreamEventError {
			t.Fatalf("stream error: %v", ev.Error)
		}
		text += ev.Delta
	}
	if text != "slow but done" {
		t.Errorf("assembled %q, want %q — the stream was cut by the sync timeout", text, "slow but done")
	}
}

// TestOpenAI_SyncTimeoutIsConfigurable: deployments that front a slow local
// model need to raise the non-streaming bound without a rebuild.
func TestOpenAI_SyncTimeoutIsConfigurable(t *testing.T) {
	o := NewOpenAI(WithOpenAIBaseURL("http://127.0.0.1:1"), WithOpenAITimeout(90*time.Second))
	if o.httpClient.Timeout != 90*time.Second {
		t.Errorf("httpClient.Timeout = %v, want 90s", o.httpClient.Timeout)
	}
}
