package v39a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestReporter_RecordCost_PostsExpectedEnvelope pins the wire shape v39a's
// /api/instances/conversation-event expects for a cost_recorded event.
// If this drifts from v39a's endpoint, that's the only test that catches
// it before runtime.
func TestReporter_RecordCost_PostsExpectedEnvelope(t *testing.T) {
	type captured struct {
		method      string
		path        string
		token       string
		contentType string
		body        map[string]any
	}
	var got captured
	var wg sync.WaitGroup
	wg.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		got.method = r.Method
		got.path = r.URL.Path
		got.token = r.Header.Get("X-V39A-Token")
		got.contentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r := &Reporter{
		BaseURL: srv.URL,
		Token:   "test-token",
		Client:  srv.Client(),
		Timeout: 2 * time.Second,
	}
	err := r.RecordCost(context.Background(), CostEvent{
		ConversationID: "conv-abc",
		Kind:           "llm",
		Vendor:         "anthropic",
		Units:          1,
		UnitCostUSD:    0.00123,
		StepType:       "classify",
		Model:          "claude-haiku-4-5-20251001",
	})
	if err != nil {
		t.Fatalf("RecordCost returned error: %v", err)
	}
	wg.Wait()

	if got.method != "POST" {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/api/instances/conversation-event" {
		t.Errorf("path = %q, want /api/instances/conversation-event", got.path)
	}
	if got.token != "test-token" {
		t.Errorf("X-V39A-Token = %q, want test-token", got.token)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.contentType)
	}
	if got.body["type"] != "cost_recorded" {
		t.Errorf("body.type = %v, want cost_recorded", got.body["type"])
	}
	if got.body["conversation_id"] != "conv-abc" {
		t.Errorf("body.conversation_id = %v, want conv-abc", got.body["conversation_id"])
	}
	if got.body["kind"] != "llm" {
		t.Errorf("body.kind = %v, want llm", got.body["kind"])
	}
	if got.body["vendor"] != "anthropic" {
		t.Errorf("body.vendor = %v, want anthropic", got.body["vendor"])
	}
	if got.body["units"].(float64) != 1.0 {
		t.Errorf("body.units = %v, want 1", got.body["units"])
	}
	if got.body["unit_cost_usd"].(float64) != 0.00123 {
		t.Errorf("body.unit_cost_usd = %v, want 0.00123", got.body["unit_cost_usd"])
	}
	if got.body["step_type"] != "classify" {
		t.Errorf("body.step_type = %v, want classify", got.body["step_type"])
	}
	if got.body["model"] != "claude-haiku-4-5-20251001" {
		t.Errorf("body.model = %v, want haiku", got.body["model"])
	}
}

func TestReporter_RecordCost_ReturnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Invalid token"}`))
	}))
	defer srv.Close()

	r := &Reporter{BaseURL: srv.URL, Token: "bad", Client: srv.Client(), Timeout: time.Second}
	err := r.RecordCost(context.Background(), CostEvent{
		ConversationID: "c",
		Kind:           "llm",
		Units:          1,
		UnitCostUSD:    0.001,
	})
	if err == nil {
		t.Fatal("RecordCost should return error on 401, got nil")
	}
}

func TestNewReporterFromEnv_ReturnsNilWhenUnconfigured(t *testing.T) {
	t.Setenv("V39A_REPORTER_URL", "")
	t.Setenv("V39A_INSTANCE_TOKEN", "")
	if r := NewReporterFromEnv(); r != nil {
		t.Errorf("expected nil when env unset, got %+v", r)
	}
}

func TestNewReporterFromEnv_ReturnsConfiguredReporter(t *testing.T) {
	t.Setenv("V39A_REPORTER_URL", "https://admin.v39a.test")
	t.Setenv("V39A_INSTANCE_TOKEN", "tok-123")
	r := NewReporterFromEnv()
	if r == nil {
		t.Fatal("expected reporter, got nil")
	}
	if r.BaseURL != "https://admin.v39a.test" {
		t.Errorf("BaseURL = %q, want https://admin.v39a.test", r.BaseURL)
	}
	if r.Token != "tok-123" {
		t.Errorf("Token = %q, want tok-123", r.Token)
	}
}

func TestNewReporterFromEnv_RequiresBothEnvVars(t *testing.T) {
	t.Setenv("V39A_REPORTER_URL", "https://admin.v39a.test")
	t.Setenv("V39A_INSTANCE_TOKEN", "")
	if r := NewReporterFromEnv(); r != nil {
		t.Error("expected nil when only URL set")
	}
	t.Setenv("V39A_REPORTER_URL", "")
	t.Setenv("V39A_INSTANCE_TOKEN", "tok")
	if r := NewReporterFromEnv(); r != nil {
		t.Error("expected nil when only token set")
	}
}
