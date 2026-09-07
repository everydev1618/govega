package tools

import (
	"context"
	"testing"
)

func TestToolNameFromContext(t *testing.T) {
	tl := NewTools()
	if err := tl.Register("echo_name", func(ctx context.Context) string {
		return ToolNameFromContext(ctx)
	}); err != nil {
		t.Fatal(err)
	}

	// The tool itself sees its name.
	got, err := tl.Execute(context.Background(), "echo_name", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "echo_name" {
		t.Errorf("tool saw name %q, want echo_name", got)
	}

	// Middleware sees the name of whichever tool is executing.
	var observed []string
	tl.Use(func(next ToolFunc) ToolFunc {
		return func(ctx context.Context, params map[string]any) (string, error) {
			observed = append(observed, ToolNameFromContext(ctx))
			return next(ctx, params)
		}
	})
	if _, err := tl.Execute(context.Background(), "echo_name", nil); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0] != "echo_name" {
		t.Errorf("middleware observed %v, want [echo_name]", observed)
	}

	// Outside any execution the accessor is empty, not a panic.
	if name := ToolNameFromContext(context.Background()); name != "" {
		t.Errorf("bare context returned %q", name)
	}
}
