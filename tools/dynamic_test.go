package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- RegisterDynamicTool ---

func TestRegisterDynamicTool(t *testing.T) {
	t.Run("exec type registers and executes", func(t *testing.T) {
		ts := NewTools()
		err := ts.RegisterDynamicTool(DynamicToolDef{
			Name:        "say_hello",
			Description: "Says hello",
			Params:      []DynamicParamDef{{Name: "name", Type: "string", Required: true}},
			Implementation: DynamicToolImpl{
				Type:    "exec",
				Command: "echo hello {{.name}}",
			},
		})
		if err != nil {
			t.Fatalf("RegisterDynamicTool: %v", err)
		}

		result, err := ts.Execute(context.Background(), "say_hello", map[string]any{"name": "world"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(result, "hello world") {
			t.Errorf("result = %q, want to contain 'hello world'", result)
		}
	})

	t.Run("unknown implementation type returns error", func(t *testing.T) {
		ts := NewTools()
		err := ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "bad_tool",
			Implementation: DynamicToolImpl{Type: "magic"},
		})
		if err == nil {
			t.Fatal("expected error for unknown type, got nil")
		}
	})

	t.Run("duplicate name returns error", func(t *testing.T) {
		ts := NewTools()
		def := DynamicToolDef{
			Name:           "dup",
			Implementation: DynamicToolImpl{Type: "exec", Command: "echo ok"},
		}
		if err := ts.RegisterDynamicTool(def); err != nil {
			t.Fatalf("first register: %v", err)
		}
		if err := ts.RegisterDynamicTool(def); err == nil {
			t.Fatal("expected error on duplicate register, got nil")
		}
	})

	t.Run("params are reflected in schema", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:        "parameterised",
			Description: "Has params",
			Params: []DynamicParamDef{
				{Name: "city", Type: "string", Required: true, Description: "The city"},
				{Name: "units", Type: "string", Enum: []string{"metric", "imperial"}},
			},
			Implementation: DynamicToolImpl{Type: "exec", Command: "echo ok"},
		})

		schemas := ts.Schema()
		if len(schemas) != 1 {
			t.Fatalf("expected 1 schema, got %d", len(schemas))
		}
		props := schemas[0].InputSchema["properties"].(map[string]any)
		if _, ok := props["city"]; !ok {
			t.Error("city should be in schema properties")
		}
		if _, ok := props["units"]; !ok {
			t.Error("units should be in schema properties")
		}
	})
}

// --- LoadFile ---

func TestLoadFile(t *testing.T) {
	t.Run("loads valid YAML and registers tool", func(t *testing.T) {
		dir := t.TempDir()
		yaml := `
name: greet
description: Greets someone
params:
  - name: name
    type: string
    required: true
implementation:
  type: exec
  command: echo hi {{.name}}
`
		path := filepath.Join(dir, "greet.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
			t.Fatal(err)
		}

		ts := NewTools()
		if err := ts.LoadFile(path); err != nil {
			t.Fatalf("LoadFile: %v", err)
		}

		result, err := ts.Execute(context.Background(), "greet", map[string]any{"name": "Alice"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(result, "Alice") {
			t.Errorf("result = %q, want to contain 'Alice'", result)
		}
	})

	t.Run("file not found returns error", func(t *testing.T) {
		ts := NewTools()
		if err := ts.LoadFile("/nonexistent/path.yaml"); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("invalid YAML returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.yaml")
		if err := os.WriteFile(path, []byte(":\t invalid: [yaml"), 0644); err != nil {
			t.Fatal(err)
		}
		ts := NewTools()
		if err := ts.LoadFile(path); err == nil {
			t.Fatal("expected error for invalid YAML")
		}
	})

	t.Run("unknown impl type in file returns error", func(t *testing.T) {
		dir := t.TempDir()
		yaml := "name: bad\nimplementation:\n  type: unknown\n"
		path := filepath.Join(dir, "bad.yaml")
		os.WriteFile(path, []byte(yaml), 0644)

		ts := NewTools()
		if err := ts.LoadFile(path); err == nil {
			t.Fatal("expected error for unknown implementation type")
		}
	})
}

// --- LoadDirectory ---

func TestLoadDirectory(t *testing.T) {
	t.Run("loads .yaml and .yml files", func(t *testing.T) {
		dir := t.TempDir()
		write := func(name, content string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
		write("alpha.yaml", "name: alpha\nimplementation:\n  type: exec\n  command: echo alpha\n")
		write("beta.yml", "name: beta\nimplementation:\n  type: exec\n  command: echo beta\n")

		ts := NewTools()
		if err := ts.LoadDirectory(dir); err != nil {
			t.Fatalf("LoadDirectory: %v", err)
		}

		names := schemaNames(ts)
		if !names["alpha"] {
			t.Error("alpha should be registered")
		}
		if !names["beta"] {
			t.Error("beta should be registered")
		}
	})

	t.Run("skips non-YAML files", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not yaml"), 0644)
		os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/bash"), 0644)

		ts := NewTools()
		if err := ts.LoadDirectory(dir); err != nil {
			t.Fatalf("LoadDirectory: %v", err)
		}
		if len(ts.Schema()) != 0 {
			t.Errorf("expected 0 tools, got %d", len(ts.Schema()))
		}
	})

	t.Run("skips subdirectories", func(t *testing.T) {
		dir := t.TempDir()
		subdir := filepath.Join(dir, "sub")
		os.Mkdir(subdir, 0755)
		os.WriteFile(filepath.Join(subdir, "nested.yaml"),
			[]byte("name: nested\nimplementation:\n  type: exec\n  command: echo ok\n"), 0644)

		ts := NewTools()
		if err := ts.LoadDirectory(dir); err != nil {
			t.Fatalf("LoadDirectory: %v", err)
		}
		if len(ts.Schema()) != 0 {
			t.Errorf("expected 0 tools (subdirs skipped), got %d", len(ts.Schema()))
		}
	})

	t.Run("directory not found returns error", func(t *testing.T) {
		ts := NewTools()
		if err := ts.LoadDirectory("/nonexistent/dir"); err == nil {
			t.Fatal("expected error for missing directory")
		}
	})
}

// --- Exec executor ---

func TestExecExecutor(t *testing.T) {
	t.Run("runs command and captures stdout", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "run_echo",
			Implementation: DynamicToolImpl{Type: "exec", Command: "echo hello"},
		})
		result, err := ts.Execute(context.Background(), "run_echo", map[string]any{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(result, "hello") {
			t.Errorf("result = %q, want to contain 'hello'", result)
		}
	})

	t.Run("interpolates params into command", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "greet",
			Params:         []DynamicParamDef{{Name: "name", Type: "string"}},
			Implementation: DynamicToolImpl{Type: "exec", Command: "echo {{.name}}"},
		})
		result, err := ts.Execute(context.Background(), "greet", map[string]any{"name": "Bob"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(result, "Bob") {
			t.Errorf("result = %q, want to contain 'Bob'", result)
		}
	})

	t.Run("returns error on non-zero exit code", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "fail_cmd",
			Implementation: DynamicToolImpl{Type: "exec", Command: "false"},
		})
		_, err := ts.Execute(context.Background(), "fail_cmd", map[string]any{})
		if err == nil {
			t.Fatal("expected error for non-zero exit, got nil")
		}
	})

	t.Run("respects quoted arguments", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "quoted",
			Implementation: DynamicToolImpl{Type: "exec", Command: "echo 'hello world'"},
		})
		result, err := ts.Execute(context.Background(), "quoted", map[string]any{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(result, "hello world") {
			t.Errorf("result = %q, want 'hello world'", result)
		}
	})
}

// --- File executors ---

func TestFileExecutors(t *testing.T) {
	t.Run("file_write then file_read round-trips content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{Name: "write_it", Implementation: DynamicToolImpl{Type: "file_write"}})
		ts.RegisterDynamicTool(DynamicToolDef{Name: "read_it", Implementation: DynamicToolImpl{Type: "file_read"}})

		if _, err := ts.Execute(context.Background(), "write_it", map[string]any{
			"path": path, "content": "hello from dynamic tool",
		}); err != nil {
			t.Fatalf("write: %v", err)
		}

		result, err := ts.Execute(context.Background(), "read_it", map[string]any{"path": path})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if result != "hello from dynamic tool" {
			t.Errorf("content = %q, want 'hello from dynamic tool'", result)
		}
	})

	t.Run("file_read missing path returns error", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{Name: "read_it", Implementation: DynamicToolImpl{Type: "file_read"}})
		if _, err := ts.Execute(context.Background(), "read_it", map[string]any{}); err == nil {
			t.Fatal("expected error for missing path")
		}
	})

	t.Run("file_write missing path returns error", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{Name: "write_it", Implementation: DynamicToolImpl{Type: "file_write"}})
		if _, err := ts.Execute(context.Background(), "write_it", map[string]any{"content": "hi"}); err == nil {
			t.Fatal("expected error for missing path")
		}
	})

	t.Run("file_write missing content returns error", func(t *testing.T) {
		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{Name: "write_it", Implementation: DynamicToolImpl{Type: "file_write"}})
		if _, err := ts.Execute(context.Background(), "write_it", map[string]any{"path": "/tmp/x"}); err == nil {
			t.Fatal("expected error for missing content")
		}
	})
}

// --- HTTP executor ---

func TestHTTPExecutor(t *testing.T) {
	t.Run("GET request returns response body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method = %q, want GET", r.Method)
			}
			w.Write([]byte(`{"status":"ok"}`))
		}))
		defer srv.Close()

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "ping",
			Implementation: DynamicToolImpl{Type: "http", URL: srv.URL},
		})

		result, err := ts.Execute(context.Background(), "ping", map[string]any{})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(result, "ok") {
			t.Errorf("result = %q, want to contain 'ok'", result)
		}
	})

	t.Run("interpolates URL with params", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Write([]byte("ok"))
		}))
		defer srv.Close()

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "get_item",
			Params:         []DynamicParamDef{{Name: "id", Type: "string"}},
			Implementation: DynamicToolImpl{Type: "http", URL: srv.URL + "/items/{{.id}}"},
		})

		ts.Execute(context.Background(), "get_item", map[string]any{"id": "42"})
		if gotPath != "/items/42" {
			t.Errorf("path = %q, want /items/42", gotPath)
		}
	})

	t.Run("POST with string body interpolates params", func(t *testing.T) {
		var gotBody string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Write([]byte("ok"))
		}))
		defer srv.Close()

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:   "create",
			Params: []DynamicParamDef{{Name: "msg", Type: "string"}},
			Implementation: DynamicToolImpl{
				Type:   "http",
				Method: "POST",
				URL:    srv.URL,
				Body:   `{"message":"{{.msg}}"}`,
			},
		})

		ts.Execute(context.Background(), "create", map[string]any{"msg": "hello"})
		if !strings.Contains(gotBody, "hello") {
			t.Errorf("body = %q, want to contain 'hello'", gotBody)
		}
	})

	t.Run("sets custom headers with interpolation", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Write([]byte("ok"))
		}))
		defer srv.Close()

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:   "auth_req",
			Params: []DynamicParamDef{{Name: "token", Type: "string"}},
			Implementation: DynamicToolImpl{
				Type:    "http",
				URL:     srv.URL,
				Headers: map[string]string{"Authorization": "Bearer {{.token}}"},
			},
		})

		ts.Execute(context.Background(), "auth_req", map[string]any{"token": "secret123"})
		if gotAuth != "Bearer secret123" {
			t.Errorf("Authorization = %q, want 'Bearer secret123'", gotAuth)
		}
	})

	t.Run("appends query params", func(t *testing.T) {
		var gotQ string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQ = r.URL.Query().Get("q")
			w.Write([]byte("ok"))
		}))
		defer srv.Close()

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:   "search",
			Params: []DynamicParamDef{{Name: "query", Type: "string"}},
			Implementation: DynamicToolImpl{
				Type:  "http",
				URL:   srv.URL,
				Query: map[string]string{"q": "{{.query}}"},
			},
		})

		ts.Execute(context.Background(), "search", map[string]any{"query": "golang"})
		if gotQ != "golang" {
			t.Errorf("query param q = %q, want 'golang'", gotQ)
		}
	})

	t.Run("4xx status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer srv.Close()

		ts := NewTools()
		ts.RegisterDynamicTool(DynamicToolDef{
			Name:           "bad_req",
			Implementation: DynamicToolImpl{Type: "http", URL: srv.URL},
		})

		if _, err := ts.Execute(context.Background(), "bad_req", map[string]any{}); err == nil {
			t.Fatal("expected error for 4xx response")
		}
	})
}

// --- parseCommand ---

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"echo hello", []string{"echo", "hello"}},
		{"echo 'hello world'", []string{"echo", "hello world"}},
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{"git commit -m 'fix bug'", []string{"git", "commit", "-m", "fix bug"}},
		{"singleword", []string{"singleword"}},
		{"", nil},
	}

	for _, tt := range tests {
		got := parseCommand(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseCommand(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i, v := range got {
			if v != tt.want[i] {
				t.Errorf("parseCommand(%q)[%d] = %q, want %q", tt.input, i, v, tt.want[i])
			}
		}
	}
}

// --- interpolateTemplate ---

func TestInterpolateTemplate(t *testing.T) {
	t.Run("no placeholders returns string unchanged", func(t *testing.T) {
		result, err := interpolateTemplate("hello world", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if result != "hello world" {
			t.Errorf("= %q, want 'hello world'", result)
		}
	})

	t.Run("replaces placeholder with param value", func(t *testing.T) {
		result, err := interpolateTemplate("hello {{.name}}", map[string]any{"name": "Alice"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "hello Alice" {
			t.Errorf("= %q, want 'hello Alice'", result)
		}
	})

	t.Run("multiple placeholders", func(t *testing.T) {
		result, err := interpolateTemplate("{{.a}}-{{.b}}", map[string]any{"a": "foo", "b": "bar"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "foo-bar" {
			t.Errorf("= %q, want 'foo-bar'", result)
		}
	})

	t.Run("invalid template syntax returns error", func(t *testing.T) {
		if _, err := interpolateTemplate("{{.unclosed", map[string]any{}); err == nil {
			t.Fatal("expected error for invalid template")
		}
	})
}

// --- helpers ---

func schemaNames(ts *Tools) map[string]bool {
	names := make(map[string]bool)
	for _, s := range ts.Schema() {
		names[s.Name] = true
	}
	return names
}
