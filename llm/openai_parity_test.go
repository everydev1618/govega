package llm

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const okOpenAIJSON = `{"id":"c1","choices":[{"message":{"role":"assistant","content":"ok"},` +
	`"finish_reason":"stop"}],"usage":{"prompt_tokens":1000000,"completion_tokens":500000}}`

// TestOpenAICostTracking verifies configured per-token pricing produces a
// non-zero CostUSD (the backend used to always report $0).
func TestOpenAICostTracking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okOpenAIJSON))
	}))
	defer srv.Close()

	o := NewOpenAI(WithOpenAIAPIKey("k"), WithOpenAIBaseURL(srv.URL),
		WithOpenAIPricing(2.00, 6.00)) // $2/M input, $6/M output

	resp, err := o.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := 2.00 + 3.00 // 1M input @ $2 + 0.5M output @ $6
	if math.Abs(resp.CostUSD-want) > 0.0001 {
		t.Errorf("CostUSD = %.4f, want %.4f", resp.CostUSD, want)
	}
}

// TestOpenAICostDefaultsToZero verifies unconfigured pricing (local models)
// reports $0 without warnings.
func TestOpenAICostDefaultsToZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okOpenAIJSON))
	}))
	defer srv.Close()

	o := NewOpenAI(WithOpenAIAPIKey("k"), WithOpenAIBaseURL(srv.URL))
	resp, err := o.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.CostUSD != 0 {
		t.Errorf("CostUSD = %.4f, want 0 for unconfigured pricing", resp.CostUSD)
	}
}

// TestOpenAIRetries5xx verifies the OpenAI backend retries transient
// failures like the Anthropic backend does.
func TestOpenAIRetries5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okOpenAIJSON))
	}))
	defer srv.Close()

	o := NewOpenAI(WithOpenAIAPIKey("k"), WithOpenAIBaseURL(srv.URL))
	o.retryBase = 1

	resp, err := o.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate should have retried past 503: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
}

// TestAnthropicStreamMessageEndCarriesCost verifies the streaming path
// reports cost on the message_end event so callers don't have to recompute
// it from a possibly-unknown model name.
func TestAnthropicStreamMessageEndCarriesCost(t *testing.T) {
	body := sseEvent("message_start", `{"message":{"usage":{"input_tokens":1000000}}}`) +
		sseEvent("content_block_start", `{"content_block":{"type":"text"}}`) +
		sseEvent("content_block_delta", `{"delta":{"type":"text_delta","text":"hi"}}`) +
		sseEvent("content_block_stop", `{}`) +
		sseEvent("message_delta", `{"usage":{"output_tokens":1000000}}`) +
		sseEvent("message_stop", `{}`)
	srv := httptest.NewServer(sseHandler(body))
	defer srv.Close()

	a := NewAnthropic(WithAPIKey("k"), WithBaseURL(srv.URL), WithModel("claude-sonnet-4-6"))
	ch, err := a.GenerateStream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	var cost float64
	for ev := range ch {
		if ev.Error != nil {
			t.Fatalf("stream error: %v", ev.Error)
		}
		if ev.Type == StreamEventMessageEnd {
			cost += ev.CostUSD
		}
	}
	want := 3.00 + 15.00 // sonnet-4-6: 1M in + 1M out
	if math.Abs(cost-want) > 0.0001 {
		t.Errorf("message_end CostUSD = %.4f, want %.4f", cost, want)
	}
}
