package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseHandler returns an http.Handler that writes the given SSE body verbatim.
func sseHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func sseEvent(event, data string) string {
	return "event: " + event + "\ndata: " + data + "\n\n"
}

func streamingClient(t *testing.T, srv *httptest.Server) *AnthropicLLM {
	t.Helper()
	return NewAnthropic(WithAPIKey("test-key"), WithBaseURL(srv.URL))
}

func drainStream(t *testing.T, ch <-chan StreamEvent) (text string, errs []error) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return text, errs
			}
			if ev.Error != nil {
				errs = append(errs, ev.Error)
			}
			if ev.Type == StreamEventContentDelta {
				text += ev.Delta
			}
		case <-deadline:
			t.Fatal("stream did not close within deadline")
		}
	}
}

// TestStreamTruncationSurfacesError verifies that a stream ending without
// message_stop (server closed early, proxy cut the connection) produces an
// error event instead of silently returning a truncated answer as complete.
func TestStreamTruncationSurfacesError(t *testing.T) {
	body := sseEvent("message_start", `{"message":{"usage":{"input_tokens":10}}}`) +
		sseEvent("content_block_start", `{"content_block":{"type":"text"}}`) +
		sseEvent("content_block_delta", `{"delta":{"type":"text_delta","text":"partial answer"}}`)
	// No content_block_stop / message_delta / message_stop — truncated.
	srv := httptest.NewServer(sseHandler(body))
	defer srv.Close()

	llm := streamingClient(t, srv)
	ch, err := llm.GenerateStream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	_, errs := drainStream(t, ch)
	if len(errs) == 0 {
		t.Error("truncated stream produced no error event; caller would treat the partial answer as complete")
	}
}

// TestStreamCompleteProducesNoError verifies a well-formed stream does not
// trip the truncation detection.
func TestStreamCompleteProducesNoError(t *testing.T) {
	body := sseEvent("message_start", `{"message":{"usage":{"input_tokens":10}}}`) +
		sseEvent("content_block_start", `{"content_block":{"type":"text"}}`) +
		sseEvent("content_block_delta", `{"delta":{"type":"text_delta","text":"hello"}}`) +
		sseEvent("content_block_stop", `{}`) +
		sseEvent("message_delta", `{"usage":{"output_tokens":5}}`) +
		sseEvent("message_stop", `{}`)
	srv := httptest.NewServer(sseHandler(body))
	defer srv.Close()

	llm := streamingClient(t, srv)
	ch, err := llm.GenerateStream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	text, errs := drainStream(t, ch)
	if len(errs) != 0 {
		t.Errorf("complete stream produced error events: %v", errs)
	}
	if text != "hello" {
		t.Errorf("stream text = %q, want %q", text, "hello")
	}
}

// TestStreamHandlesLongLines verifies SSE data lines larger than
// bufio.Scanner's 64KB default limit are parsed rather than silently
// truncating the stream.
func TestStreamHandlesLongLines(t *testing.T) {
	bigText := strings.Repeat("x", 200*1024) // 200KB, past the 64KB default cap
	body := sseEvent("message_start", `{"message":{"usage":{"input_tokens":10}}}`) +
		sseEvent("content_block_start", `{"content_block":{"type":"text"}}`) +
		sseEvent("content_block_delta", fmt.Sprintf(`{"delta":{"type":"text_delta","text":"%s"}}`, bigText)) +
		sseEvent("content_block_stop", `{}`) +
		sseEvent("message_delta", `{"usage":{"output_tokens":5}}`) +
		sseEvent("message_stop", `{}`)
	srv := httptest.NewServer(sseHandler(body))
	defer srv.Close()

	llm := streamingClient(t, srv)
	ch, err := llm.GenerateStream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	text, errs := drainStream(t, ch)
	if len(errs) != 0 {
		t.Errorf("long-line stream produced error events: %v", errs)
	}
	if len(text) != len(bigText) {
		t.Errorf("received %d bytes of text, want %d (long SSE line dropped)", len(text), len(bigText))
	}
}

// TestStreamingPathHasNoOverallTimeout verifies the streaming HTTP client
// carries no whole-request timeout — a 5-minute Client.Timeout silently cuts
// long streams mid-answer. Cancellation on streams is the caller's context.
func TestStreamingPathHasNoOverallTimeout(t *testing.T) {
	a := NewAnthropic(WithAPIKey("test-key"))
	if a.streamClient == nil {
		t.Fatal("no dedicated streaming client configured")
	}
	if a.streamClient.Timeout != 0 {
		t.Errorf("streaming client Timeout = %v, want 0 (streams outlive any fixed timeout)", a.streamClient.Timeout)
	}
	// Non-streaming path keeps its bounded timeout.
	if a.httpClient.Timeout != DefaultAnthropicTimeout {
		t.Errorf("non-streaming client Timeout = %v, want %v", a.httpClient.Timeout, DefaultAnthropicTimeout)
	}
}
