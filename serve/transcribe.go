package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// transcriber speaks the OpenAI-compatible audio/transcriptions endpoint
// (Whisper). Configured by env so it transparently rides whatever proxy
// (LiteLLM, etc.) the rest of vega is already using for OPENAI_BASE_URL.
type transcriber struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// newDefaultTranscriber builds a transcriber from env. Returns a struct even
// if apiKey is empty — Transcribe surfaces a clear error in that case so the
// caller can pass an informative message back to the user.
func newDefaultTranscriber() *transcriber {
	apiKey := os.Getenv("VEGA_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	baseURL := strings.TrimRight(os.Getenv("OPENAI_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := os.Getenv("VEGA_TRANSCRIBE_MODEL")
	if model == "" {
		model = "whisper-1"
	}
	return &transcriber{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}
}

// Transcribe POSTs audio bytes as multipart/form-data to {baseURL}/audio/transcriptions
// and returns the recognised text. filename is sent as the multipart filename
// so the server can detect the audio format from its extension.
func (t *transcriber) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	if t.apiKey == "" {
		return "", errors.New("transcribe: no API key set (OPENAI_API_KEY or VEGA_API_KEY)")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("transcribe: build form: %w", err)
	}
	if _, err := fw.Write(audio); err != nil {
		return "", fmt.Errorf("transcribe: write audio: %w", err)
	}
	if err := mw.WriteField("model", t.model); err != nil {
		return "", fmt.Errorf("transcribe: write model field: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("transcribe: close multipart: %w", err)
	}

	url := t.baseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("transcribe: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcribe: http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("transcribe: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("transcribe: decode: %w", err)
	}
	return strings.TrimSpace(parsed.Text), nil
}
