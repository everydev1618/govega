package tools

import (
	"context"
	"strings"
	"testing"
)

// TestExecuteRecoversFromToolPanic ensures a panic inside a tool function is
// converted to an error instead of crashing the server process. Regression
// test for C3.
func TestExecuteRecoversFromToolPanic(t *testing.T) {
	tl := NewTools()
	tl.Register("boom", ToolDef{
		Description: "panics",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			panic("kaboom")
		},
	})

	result, err := tl.Execute(context.Background(), "boom", map[string]any{})
	if err == nil {
		t.Fatalf("Execute() returned nil error for panicking tool; result=%q", result)
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error = %v, want it to mention the panic", err)
	}
}

// TestExecuteMissingRequiredParamNoPanic ensures a built-in that assumes a
// param type does not crash the server when the model omits or mistypes it.
func TestExecuteMissingRequiredParamNoPanic(t *testing.T) {
	tl := NewTools(WithSandbox(t.TempDir()))
	tl.RegisterBuiltins()

	// "path" is required but omitted — must not panic.
	_, err := tl.Execute(context.Background(), "write_file", map[string]any{
		"content": "hello",
	})
	if err == nil {
		t.Fatal("Execute() returned nil error for write_file with missing path, want error")
	}

	// "content" present but wrong type on a path that exists — must not panic.
	_, err = tl.Execute(context.Background(), "write_file", map[string]any{
		"path":    123,
		"content": "hello",
	})
	if err == nil {
		t.Fatal("Execute() returned nil error for write_file with non-string path, want error")
	}
}
