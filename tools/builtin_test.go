package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileReturnsURL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "mysite"), 0755)

	tools := NewTools(WithSandbox(dir), WithBaseURL("http://localhost:3001"))
	tools.RegisterBuiltins()

	// Write a file
	result, err := tools.Execute(context.Background(), "write_file", map[string]any{
		"path":    "mysite/index.html",
		"content": "<h1>Hello</h1>",
	})
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	if !strings.Contains(result, "http://localhost:3001/workspace/mysite/index.html") {
		t.Errorf("expected URL in response, got: %s", result)
	}

	// Verify file was actually written
	data, err := os.ReadFile(filepath.Join(dir, "mysite/index.html"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != "<h1>Hello</h1>" {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestWriteFileReturnsURLWithProject(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cto-tools", "stackctl-site"), 0755)

	tools := NewTools(WithSandbox(dir), WithBaseURL("http://localhost:3001"))
	tools.RegisterBuiltins()
	tools.SetActiveProject("cto-tools")

	result, err := tools.Execute(context.Background(), "write_file", map[string]any{
		"path":    "stackctl-site/index.html",
		"content": "<h1>Hello</h1>",
	})
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	if !strings.Contains(result, "http://localhost:3001/workspace/cto-tools/stackctl-site/index.html") {
		t.Errorf("expected URL with project in response, got: %s", result)
	}
}

func TestWriteFileNoURLWithoutBaseURL(t *testing.T) {
	dir := t.TempDir()

	tools := NewTools(WithSandbox(dir))
	tools.RegisterBuiltins()

	result, err := tools.Execute(context.Background(), "write_file", map[string]any{
		"path":    "test.txt",
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	if strings.Contains(result, "http") {
		t.Errorf("expected no URL without base URL, got: %s", result)
	}
}
