package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// transcribeRequest builds a multipart POST with an audio blob under the
// given field name.
func transcribeRequest(t *testing.T, field, filename string, audio []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(audio); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcribe", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestTranscribe_MissingFile400(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	s := &Server{}
	req := transcribeRequest(t, "wrong_field", "clip.webm", []byte("audio"))
	w := httptest.NewRecorder()
	s.handleTranscribe(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestTranscribe_NotConfigured503(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VEGA_API_KEY", "")
	s := &Server{}
	req := transcribeRequest(t, "file", "clip.webm", []byte("audio"))
	w := httptest.NewRecorder()
	s.handleTranscribe(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503; body=%s", w.Code, w.Body.String())
	}
}

func TestTranscribe_HappyPath(t *testing.T) {
	var gotModel, gotFilename string
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("path=%s, want /audio/transcriptions", r.URL.Path)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Fatalf("parse upstream multipart: %v", err)
		}
		gotModel = r.FormValue("model")
		if _, header, err := r.FormFile("file"); err == nil {
			gotFilename = header.Filename
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text": "  hello from whisper  "}`)
	}))
	defer whisper.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", whisper.URL)
	t.Setenv("VEGA_TRANSCRIBE_MODEL", "")

	s := &Server{}
	req := transcribeRequest(t, "file", "dictation.webm", []byte("fake-opus-bytes"))
	w := httptest.NewRecorder()
	s.handleTranscribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Text != "hello from whisper" {
		t.Errorf("text=%q, want trimmed whisper output", resp.Text)
	}
	if gotModel != "whisper-1" {
		t.Errorf("model=%q, want whisper-1 default", gotModel)
	}
	if gotFilename != "dictation.webm" {
		t.Errorf("filename=%q, want dictation.webm passed through", gotFilename)
	}
}

func TestTranscribe_UpstreamError502(t *testing.T) {
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "whisper exploded")
	}))
	defer whisper.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", whisper.URL)

	s := &Server{}
	req := transcribeRequest(t, "file", "clip.webm", []byte("audio"))
	w := httptest.NewRecorder()
	s.handleTranscribe(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status=%d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "whisper exploded") {
		t.Errorf("expected upstream error surfaced, got %s", w.Body.String())
	}
}
