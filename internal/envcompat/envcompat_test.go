package envcompat

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestGet_PrefersNewName(t *testing.T) {
	t.Setenv("VEGA_TENANT_ID", "new-value")
	t.Setenv("APEX_TENANT_ID", "old-value")
	if got := Get("VEGA_TENANT_ID"); got != "new-value" {
		t.Errorf("Get with both set returned %q, want %q", got, "new-value")
	}
}

func TestGet_FallsBackToLegacy(t *testing.T) {
	t.Setenv("VEGA_TENANT_ID", "")
	t.Setenv("APEX_TENANT_ID", "old-value")
	resetForTest()
	if got := Get("VEGA_TENANT_ID"); got != "old-value" {
		t.Errorf("Get with only legacy set returned %q, want %q", got, "old-value")
	}
}

func TestGet_BothEmptyReturnsEmpty(t *testing.T) {
	t.Setenv("VEGA_TENANT_ID", "")
	t.Setenv("APEX_TENANT_ID", "")
	if got := Get("VEGA_TENANT_ID"); got != "" {
		t.Errorf("Get with neither set returned %q, want \"\"", got)
	}
}

func TestGet_LegacyFallbackLogsDeprecationOnce(t *testing.T) {
	t.Setenv("VEGA_TENANT_ID", "")
	t.Setenv("APEX_TENANT_ID", "old-value")
	resetForTest()

	// Redirect slog to a buffer so the test can assert on the warning.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	_ = Get("VEGA_TENANT_ID")
	_ = Get("VEGA_TENANT_ID") // second call must NOT log again

	out := buf.String()
	if !strings.Contains(out, "APEX_TENANT_ID") || !strings.Contains(out, "VEGA_TENANT_ID") {
		t.Errorf("expected deprecation warning to name both env vars, got: %s", out)
	}
	// Count occurrences of "APEX_TENANT_ID" in log output — should appear
	// exactly once across the two Get calls.
	if n := strings.Count(out, "APEX_TENANT_ID"); n != 1 {
		t.Errorf("expected exactly 1 deprecation warning, got %d:\n%s", n, out)
	}
}

func TestGet_NewNameAloneDoesNotLog(t *testing.T) {
	t.Setenv("VEGA_TENANT_ID", "new-value")
	t.Setenv("APEX_TENANT_ID", "")
	resetForTest()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	_ = Get("VEGA_TENANT_ID")
	if strings.Contains(buf.String(), "APEX_TENANT_ID") {
		t.Errorf("expected no deprecation warning when only new name is set, got: %s", buf.String())
	}
}

func TestGet_PanicsOnNonVEGAPrefix(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for non-VEGA prefix")
		}
	}()
	_ = Get("APEX_TENANT_ID") // wrong direction — should be a programmer error
}
