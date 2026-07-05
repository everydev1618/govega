package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleTranscribe drives the mic endpoint against a mock Whisper server:
// audio in → transcript JSON out.
func TestHandleTranscribe(t *testing.T) {
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello from voice"})
	}))
	defer whisper.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", whisper.URL)

	s := &Server{}
	req := httptest.NewRequest("POST", "/api/v1/transcribe?filename=clip.webm", strings.NewReader("fake-audio-bytes"))
	rec := httptest.NewRecorder()
	s.handleTranscribe(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v (%s)", err, rec.Body.String())
	}
	if out.Text != "hello from voice" {
		t.Errorf("transcript = %q", out.Text)
	}
}

func TestHandleTranscribeEmptyBody(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("POST", "/api/v1/transcribe", strings.NewReader(""))
	rec := httptest.NewRecorder()
	s.handleTranscribe(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty body should be 400, got %d", rec.Code)
	}
}
