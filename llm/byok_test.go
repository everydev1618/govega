package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureKeyServer returns a test server that records the x-api-key header
// of the most recent request and replies with a minimal valid response.
func captureKeyServer(t *testing.T, got *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "claude-sonnet-4-6",
			"content":       []any{map[string]any{"type": "text", "text": "ok"}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
}

// When ctx carries a per-request Anthropic key, outbound calls must use
// that key — not the construct-time fallback.
func TestAnthropicAPIKeyFromContextOverridesStaticKey(t *testing.T) {
	var got string
	srv := captureKeyServer(t, &got)
	defer srv.Close()

	a := NewAnthropic(WithAPIKey("static-fallback"), WithBaseURL(srv.URL))

	ctx := WithAPIKeyContext(context.Background(), "per-user-key")
	_, err := a.Generate(ctx, []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if got != "per-user-key" {
		t.Errorf("x-api-key header = %q, want %q", got, "per-user-key")
	}
}

// When ctx has no per-request key, the construct-time key is used.
func TestAnthropicAPIKeyFallsBackToStaticKey(t *testing.T) {
	var got string
	srv := captureKeyServer(t, &got)
	defer srv.Close()

	a := NewAnthropic(WithAPIKey("static-fallback"), WithBaseURL(srv.URL))

	_, err := a.Generate(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if got != "static-fallback" {
		t.Errorf("x-api-key header = %q, want %q", got, "static-fallback")
	}
}

// APIKeyFromContext returns "" when no key is attached.
func TestAPIKeyFromContextEmpty(t *testing.T) {
	if k := APIKeyFromContext(context.Background()); k != "" {
		t.Errorf("expected empty key, got %q", k)
	}
}

// APIKeyFromContext returns the value WithAPIKeyContext stored.
func TestAPIKeyFromContextRoundTrip(t *testing.T) {
	ctx := WithAPIKeyContext(context.Background(), "abc-123")
	if k := APIKeyFromContext(ctx); k != "abc-123" {
		t.Errorf("got %q, want %q", k, "abc-123")
	}
}
