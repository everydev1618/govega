package llm

import (
	"net/http"
	"strings"
	"testing"
)

// A non-200 from the API logs the outbound request headers to help debug a
// rejected call. Those headers carry the caller's credentials, and on a
// multi-tenant host the log is not a safe place for them.
func TestRedactHeadersHidesCredentials(t *testing.T) {
	const secret = "sk-ant-api03-REAL-SECRET-VALUE"

	h := http.Header{}
	h.Set("X-Api-Key", secret)
	h.Set("Authorization", "Bearer "+secret)
	h.Set("Content-Type", "application/json")
	h.Set("Anthropic-Version", "2023-06-01")

	got := redactHeaders(h)

	if strings.Contains(got, secret) {
		t.Fatalf("redactHeaders leaked the credential: %s", got)
	}
	if strings.Contains(got, "sk-ant") {
		t.Errorf("redactHeaders leaked a key prefix: %s", got)
	}

	// Non-sensitive headers stay readable — the whole point of the log line.
	if !strings.Contains(got, "application/json") {
		t.Errorf("redactHeaders dropped Content-Type: %s", got)
	}
	if !strings.Contains(got, "2023-06-01") {
		t.Errorf("redactHeaders dropped Anthropic-Version: %s", got)
	}

	// The sensitive header should still be visibly present, just masked, so a
	// missing-key bug is still diagnosable from the log.
	if !strings.Contains(strings.ToLower(got), "api-key") {
		t.Errorf("redactHeaders dropped the X-Api-Key name entirely: %s", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Errorf("redactHeaders did not mark the value redacted: %s", got)
	}
}

func TestRedactHeadersIsCaseInsensitive(t *testing.T) {
	// http.Header canonicalizes, but a header set directly on the map does not
	// go through that path.
	h := http.Header{"x-api-key": []string{"sk-ant-lowercase"}}
	if got := redactHeaders(h); strings.Contains(got, "sk-ant-lowercase") {
		t.Errorf("lowercase header key leaked: %s", got)
	}
}

func TestRedactHeadersEmpty(t *testing.T) {
	if got := redactHeaders(http.Header{}); got == "" {
		t.Error("redactHeaders returned empty string for empty headers")
	}
}
