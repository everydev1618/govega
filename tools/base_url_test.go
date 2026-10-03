package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The base URL baked in at boot is a guess on a self-hosted box — the server
// has no way to know its own public hostname until a browser tells it. When a
// request supplies the real origin, the deliverable link write_file reports
// must use it, not the boot-time guess the agent would otherwise quote back
// to the user verbatim.
func TestWriteFileURLPrefersContextBaseURL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0755)

	tools := NewTools(WithSandbox(dir), WithBaseURL("http://localhost:8822"))
	tools.RegisterBuiltins()

	ctx := ContextWithBaseURL(context.Background(), "http://vega.const")
	result, err := tools.Execute(ctx, "write_file", map[string]any{
		"path":    "docs/moneyball-agents.md",
		"content": "# draft",
	})
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	if !strings.Contains(result, "http://vega.const/workspace/docs/moneyball-agents.md") {
		t.Errorf("expected the request origin in the URL, got: %s", result)
	}
	if strings.Contains(result, "localhost") {
		t.Errorf("boot-time localhost guess leaked into the deliverable URL: %s", result)
	}
}

// Without a per-request origin — Telegram, cron, a dispatched sub-agent —
// the configured base URL still applies.
func TestWriteFileURLFallsBackToConfiguredBaseURL(t *testing.T) {
	dir := t.TempDir()

	tools := NewTools(WithSandbox(dir), WithBaseURL("http://vega.const"))
	tools.RegisterBuiltins()

	result, err := tools.Execute(context.Background(), "write_file", map[string]any{
		"path":    "notes.md",
		"content": "hi",
	})
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}
	if !strings.Contains(result, "http://vega.const/workspace/notes.md") {
		t.Errorf("expected configured base URL, got: %s", result)
	}
}

func TestBaseURLFromContext(t *testing.T) {
	if got := BaseURLFrom(context.Background()); got != "" {
		t.Errorf("BaseURLFrom(empty) = %q, want \"\"", got)
	}
	ctx := ContextWithBaseURL(context.Background(), "http://vega.const/")
	if got := BaseURLFrom(ctx); got != "http://vega.const" {
		t.Errorf("BaseURLFrom = %q, want trailing slash trimmed", got)
	}
	// An empty origin must not install an override that would blank out the
	// configured base URL downstream.
	if got := BaseURLFrom(ContextWithBaseURL(context.Background(), "")); got != "" {
		t.Errorf("BaseURLFrom(empty override) = %q", got)
	}
}
