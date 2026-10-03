package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// okAnthropicSSE is a complete streamed "ok" message. Generate fetches over
// the streaming endpoint, so its fakes speak SSE.
var okAnthropicSSE = sseEvent("message_start", `{"message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":5}}}`) +
	sseEvent("content_block_start", `{"index":0,"content_block":{"type":"text"}}`) +
	sseEvent("content_block_delta", `{"index":0,"delta":{"type":"text_delta","text":"ok"}}`) +
	sseEvent("content_block_stop", `{"index":0}`) +
	sseEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`) +
	sseEvent("message_stop", `{}`)

// flakyServer fails the first n requests with the given status (or by
// dropping the connection when status == 0), then serves the SSE/JSON body.
func flakyServer(t *testing.T, failures []int, body string, contentType string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		if n < len(failures) {
			status := failures[n]
			if status == 0 {
				// Transport-level failure: drop the connection mid-request.
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Error("server does not support hijacking")
					return
				}
				conn, _, _ := hj.Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"transient"}}`))
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	return srv, &calls
}

func fastRetryClient(t *testing.T, srv *httptest.Server) *AnthropicLLM {
	t.Helper()
	a := NewAnthropic(WithAPIKey("k"), WithBaseURL(srv.URL))
	a.retryBase = 1 // ~instant backoff in tests
	return a
}

// TestGenerateRetries5xx verifies transient server errors on the
// non-streaming path are retried instead of failing the call outright.
func TestGenerateRetries5xx(t *testing.T) {
	srv, calls := flakyServer(t, []int{500, 502}, okAnthropicSSE, "text/event-stream")
	defer srv.Close()

	a := fastRetryClient(t, srv)
	resp, err := a.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate should have retried past 5xx: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server saw %d requests, want 3", got)
	}
}

// TestGenerateRetriesTransportError verifies a dropped connection is retried.
func TestGenerateRetriesTransportError(t *testing.T) {
	srv, calls := flakyServer(t, []int{0}, okAnthropicSSE, "text/event-stream")
	defer srv.Close()

	a := fastRetryClient(t, srv)
	resp, err := a.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Generate should have retried the transport error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
}

// TestGenerateDoesNotRetry4xx verifies client errors fail immediately.
func TestGenerateDoesNotRetry4xx(t *testing.T) {
	srv, calls := flakyServer(t, []int{400, 400, 400}, okAnthropicSSE, "text/event-stream")
	defer srv.Close()

	a := fastRetryClient(t, srv)
	if _, err := a.Generate(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil); err == nil {
		t.Fatal("Generate should fail on 400")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1 (4xx must not retry)", got)
	}
}

// TestGenerateStreamRetries5xx verifies transient errors before the stream
// opens are retried on the streaming path too.
func TestGenerateStreamRetries5xx(t *testing.T) {
	body := sseEvent("message_start", `{"message":{"usage":{"input_tokens":10}}}`) +
		sseEvent("content_block_start", `{"content_block":{"type":"text"}}`) +
		sseEvent("content_block_delta", `{"delta":{"type":"text_delta","text":"hello"}}`) +
		sseEvent("content_block_stop", `{}`) +
		sseEvent("message_delta", `{"usage":{"output_tokens":5}}`) +
		sseEvent("message_stop", `{}`)
	srv, calls := flakyServer(t, []int{503}, body, "text/event-stream")
	defer srv.Close()

	a := fastRetryClient(t, srv)
	ch, err := a.GenerateStream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	text, errs := drainStream(t, ch)
	if len(errs) != 0 {
		t.Fatalf("stream errors after 5xx retry: %v", errs)
	}
	if text != "hello" {
		t.Errorf("text = %q", text)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
}
