package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// vapiTestServer spins up an httptest server that mocks the Vapi REST API,
// rewrites vapiAPIBase to point at it, and returns a cleanup func.
func vapiTestServer(t *testing.T, h http.Handler) (cleanup func()) {
	t.Helper()
	ts := httptest.NewServer(h)
	prevBase := vapiAPIBase
	prevKey := os.Getenv("VAPI_API_KEY")
	vapiAPIBase = ts.URL
	os.Setenv("VAPI_API_KEY", "test-vapi-key")
	return func() {
		ts.Close()
		vapiAPIBase = prevBase
		if prevKey == "" {
			os.Unsetenv("VAPI_API_KEY")
		} else {
			os.Setenv("VAPI_API_KEY", prevKey)
		}
	}
}

func TestVapiServerToolDefinitions(t *testing.T) {
	srv := vapiServer()
	if srv == nil {
		t.Fatal("vapiServer() returned nil")
	}
	expected := []string{"start_call", "get_call", "list_calls", "list_assistants"}
	for _, name := range expected {
		td, ok := srv.tools[name]
		if !ok {
			t.Errorf("missing tool %q", name)
			continue
		}
		if td.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}
		if td.Fn == nil {
			t.Errorf("tool %q has nil Fn", name)
		}
	}
	if len(srv.tools) != len(expected) {
		t.Errorf("expected %d tools, got %d", len(expected), len(srv.tools))
	}
}

func TestVapiStartCallRequiredParams(t *testing.T) {
	td := vapiServer().tools["start_call"]
	for _, name := range []string{"assistant_id", "to"} {
		p, ok := td.Params[name]
		if !ok {
			t.Errorf("start_call missing %q param", name)
			continue
		}
		if !p.Required {
			t.Errorf("start_call %q param should be required", name)
		}
	}
}

func TestVapiStartCallSuccess(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/call" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-vapi-key" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer test-vapi-key")
		}
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if got["assistantId"] != "asst_iris" {
			t.Errorf("assistantId in body = %v, want asst_iris", got["assistantId"])
		}
		if got["phoneNumberId"] != "pn_default" {
			t.Errorf("phoneNumberId in body = %v, want pn_default (from VAPI_DEFAULT_PHONE_NUMBER_ID)", got["phoneNumberId"])
		}
		customer, _ := got["customer"].(map[string]any)
		if customer["number"] != "+15551234567" {
			t.Errorf("customer.number = %v, want +15551234567", customer["number"])
		}
		overrides, _ := got["assistantOverrides"].(map[string]any)
		vars, _ := overrides["variableValues"].(map[string]any)
		if vars["tenant_id"] != "acme" {
			t.Errorf("variableValues.tenant_id = %v, want acme", vars["tenant_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"call_abc","status":"queued"}`)
	}))
	defer cleanup()

	os.Setenv("VAPI_DEFAULT_PHONE_NUMBER_ID", "pn_default")
	defer os.Unsetenv("VAPI_DEFAULT_PHONE_NUMBER_ID")

	out, err := vapiStartCall(context.Background(), map[string]any{
		"assistant_id": "asst_iris",
		"to":           "+15551234567",
		"context":      map[string]any{"tenant_id": "acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "call_abc") {
		t.Errorf("expected call_abc in output, got %q", out)
	}
}

func TestVapiStartCallExplicitPhoneNumberOverridesDefault(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if got["phoneNumberId"] != "pn_explicit" {
			t.Errorf("phoneNumberId = %v, want pn_explicit", got["phoneNumberId"])
		}
		_, _ = io.WriteString(w, `{"id":"call_xyz","status":"queued"}`)
	}))
	defer cleanup()

	os.Setenv("VAPI_DEFAULT_PHONE_NUMBER_ID", "pn_default")
	defer os.Unsetenv("VAPI_DEFAULT_PHONE_NUMBER_ID")

	_, err := vapiStartCall(context.Background(), map[string]any{
		"assistant_id":    "asst_iris",
		"to":              "+15551234567",
		"phone_number_id": "pn_explicit",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVapiStartCallMissingPhoneNumber(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be reached when no phone_number_id is configured")
	}))
	defer cleanup()

	// Ensure no default is set in env.
	os.Unsetenv("VAPI_DEFAULT_PHONE_NUMBER_ID")

	_, err := vapiStartCall(context.Background(), map[string]any{
		"assistant_id": "asst_iris",
		"to":           "+15551234567",
	})
	if err == nil {
		t.Fatal("expected error when no phone_number_id is provided and no default is set")
	}
	if !strings.Contains(err.Error(), "phone_number_id") {
		t.Errorf("expected phone_number_id mention in error, got: %s", err)
	}
}

func TestVapiStartCallMissingRequired(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be reached when required params are missing")
	}))
	defer cleanup()

	if _, err := vapiStartCall(context.Background(), map[string]any{"to": "+15551234567"}); err == nil {
		t.Error("expected error when assistant_id is missing")
	}
	if _, err := vapiStartCall(context.Background(), map[string]any{"assistant_id": "asst_iris"}); err == nil {
		t.Error("expected error when to is missing")
	}
}

func TestVapiStartCallAPIError(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	}))
	defer cleanup()

	os.Setenv("VAPI_DEFAULT_PHONE_NUMBER_ID", "pn_default")
	defer os.Unsetenv("VAPI_DEFAULT_PHONE_NUMBER_ID")

	_, err := vapiStartCall(context.Background(), map[string]any{
		"assistant_id": "asst_iris",
		"to":           "+15551234567",
	})
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error, got %s", err)
	}
}

func TestVapiGetCall(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/call/call_abc" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"call_abc","status":"ended","transcript":"hi there"}`)
	}))
	defer cleanup()

	out, err := vapiGetCall(context.Background(), map[string]any{"call_id": "call_abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ended") || !strings.Contains(out, "hi there") {
		t.Errorf("expected status + transcript in output, got %q", out)
	}
}

func TestVapiGetCallMissingID(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server without call_id")
	}))
	defer cleanup()
	if _, err := vapiGetCall(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error when call_id is missing")
	}
}

func TestVapiListCalls(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("limit") != "5" {
			t.Errorf("limit query = %q, want 5", q.Get("limit"))
		}
		if q.Get("assistantId") != "asst_iris" {
			t.Errorf("assistantId query = %q, want asst_iris", q.Get("assistantId"))
		}
		_, _ = io.WriteString(w, `[{"id":"c1","status":"ended"},{"id":"c2","status":"ended"}]`)
	}))
	defer cleanup()

	out, err := vapiListCalls(context.Background(), map[string]any{
		"limit":        float64(5),
		"assistant_id": "asst_iris",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c1") || !strings.Contains(out, "c2") {
		t.Errorf("expected both call ids in output, got %q", out)
	}
}

func TestVapiListAssistants(t *testing.T) {
	cleanup := vapiTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assistant" {
			t.Errorf("unexpected path %s, want /assistant", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"id":"asst_iris","name":"Iris"},{"id":"asst_drew","name":"Drew"}]`)
	}))
	defer cleanup()

	out, err := vapiListAssistants(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Iris") || !strings.Contains(out, "Drew") {
		t.Errorf("expected both assistants in output, got %q", out)
	}
}

func TestVapiMissingAPIKey(t *testing.T) {
	prevBase := vapiAPIBase
	prevKey := os.Getenv("VAPI_API_KEY")
	vapiAPIBase = "http://localhost:0"
	os.Unsetenv("VAPI_API_KEY")
	defer func() {
		vapiAPIBase = prevBase
		if prevKey != "" {
			os.Setenv("VAPI_API_KEY", prevKey)
		}
	}()

	_, err := vapiListAssistants(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when VAPI_API_KEY is missing")
	}
	if !strings.Contains(err.Error(), "VAPI_API_KEY") {
		t.Errorf("expected VAPI_API_KEY in error, got %s", err)
	}
}

func TestVapiConnectBuiltinServer(t *testing.T) {
	tl := NewTools()
	n, err := tl.ConnectBuiltinServer(context.Background(), "vapi")
	if err != nil {
		t.Fatalf("ConnectBuiltinServer: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 tools registered, got %d", n)
	}
	schemas := tl.Schema()
	want := map[string]bool{
		"vapi__start_call":      false,
		"vapi__get_call":        false,
		"vapi__list_calls":      false,
		"vapi__list_assistants": false,
	}
	for _, s := range schemas {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected %s in registered tools", name)
		}
	}
}
