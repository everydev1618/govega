package serve

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscribeAudio_PostsMultipartAndReturnsText(t *testing.T) {
	var (
		gotAuth      string
		gotFilename  string
		gotModel     string
		gotFileBytes []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")

		ct := r.Header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(ct)
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Fatalf("expected multipart content-type, got %q (err=%v)", ct, err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("multipart read: %v", err)
			}
			switch part.FormName() {
			case "file":
				gotFilename = part.FileName()
				gotFileBytes, _ = io.ReadAll(part)
			case "model":
				b, _ := io.ReadAll(part)
				gotModel = string(b)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello world"})
	}))
	defer srv.Close()

	tr := &transcriber{
		apiKey:     "sk-test",
		baseURL:    srv.URL,
		model:      "whisper-1",
		httpClient: srv.Client(),
	}

	out, err := tr.Transcribe(context.Background(), []byte("OggS-fake-audio"), "voice.ogg")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if out != "hello world" {
		t.Errorf("transcript = %q, want %q", out, "hello world")
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q, want %q", gotAuth, "Bearer sk-test")
	}
	if gotFilename != "voice.ogg" {
		t.Errorf("filename = %q, want %q", gotFilename, "voice.ogg")
	}
	if gotModel != "whisper-1" {
		t.Errorf("model = %q, want %q", gotModel, "whisper-1")
	}
	if string(gotFileBytes) != "OggS-fake-audio" {
		t.Errorf("file bytes = %q, want %q", gotFileBytes, "OggS-fake-audio")
	}
}

func TestTranscribeAudio_MissingAPIKey(t *testing.T) {
	tr := &transcriber{apiKey: "", baseURL: "http://unused", model: "whisper-1", httpClient: http.DefaultClient}
	_, err := tr.Transcribe(context.Background(), []byte("x"), "voice.ogg")
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestTranscribeAudio_NonOKResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	tr := &transcriber{
		apiKey:     "sk-test",
		baseURL:    srv.URL,
		model:      "whisper-1",
		httpClient: srv.Client(),
	}
	_, err := tr.Transcribe(context.Background(), []byte("x"), "voice.ogg")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "boom") && !strings.Contains(err.Error(), "500") {
		t.Errorf("error should surface server detail; got %v", err)
	}
}

func TestNewDefaultTranscriber_PicksUpEnv(t *testing.T) {
	t.Setenv("VEGA_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")
	t.Setenv("VEGA_TRANSCRIBE_MODEL", "whisper-foo")

	tr := newDefaultTranscriber()
	if tr.apiKey != "sk-from-env" {
		t.Errorf("apiKey = %q, want %q", tr.apiKey, "sk-from-env")
	}
	if tr.baseURL != "https://example.test/v1" {
		t.Errorf("baseURL = %q, want %q", tr.baseURL, "https://example.test/v1")
	}
	if tr.model != "whisper-foo" {
		t.Errorf("model = %q, want %q", tr.model, "whisper-foo")
	}
}
