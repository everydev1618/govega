package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "Hello, world!")
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Test Page</title></head><body><h1>Hello</h1><p>This is a test.</p><script>alert('x')</script></body></html>`)
		case "/long":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, strings.Repeat("abcdefghij", 100)) // 1000 chars
		case "/error":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.Error(w, "unknown", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	ctx := context.Background()

	t.Run("plain text", func(t *testing.T) {
		result, err := fetchToolFunc(ctx, map[string]any{"url": server.URL + "/plain"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result, "Hello, world!") {
			t.Errorf("expected content, got: %s", result)
		}
	})

	t.Run("html stripping", func(t *testing.T) {
		result, err := fetchToolFunc(ctx, map[string]any{"url": server.URL + "/html"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result, "Title: Test Page") {
			t.Errorf("expected title, got: %s", result)
		}
		if strings.Contains(result, "<h1>") {
			t.Error("HTML tags should be stripped")
		}
		if strings.Contains(result, "alert") {
			t.Error("script content should be removed")
		}
		if !strings.Contains(result, "Hello") {
			t.Error("body text should be preserved")
		}
	})

	t.Run("raw mode", func(t *testing.T) {
		result, err := fetchToolFunc(ctx, map[string]any{"url": server.URL + "/html", "raw": true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result, "<h1>Hello</h1>") {
			t.Errorf("raw mode should preserve HTML, got: %s", result)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, err := fetchToolFunc(ctx, map[string]any{
			"url":         server.URL + "/long",
			"max_length":  float64(50),
			"start_index": float64(0),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result, "Truncated") {
			t.Error("should indicate truncation")
		}
		if !strings.Contains(result, "start_index=50") {
			t.Error("should suggest next start_index")
		}
	})

	t.Run("start_index", func(t *testing.T) {
		result, err := fetchToolFunc(ctx, map[string]any{
			"url":         server.URL + "/long",
			"max_length":  float64(10),
			"start_index": float64(5),
		})
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(result, "\n")
		last := lines[len(lines)-1]
		if !strings.HasPrefix(last, "fghij") {
			t.Errorf("expected content starting at offset 5, got last line: %s", last)
		}
	})

	t.Run("HTTP error", func(t *testing.T) {
		_, err := fetchToolFunc(ctx, map[string]any{"url": server.URL + "/error"})
		if err == nil {
			t.Fatal("expected error for 404")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("expected 404 in error, got: %s", err)
		}
	})

	t.Run("missing url", func(t *testing.T) {
		_, err := fetchToolFunc(ctx, map[string]any{})
		if err == nil {
			t.Fatal("expected error for missing url")
		}
	})
}

func TestHasBuiltinServer(t *testing.T) {
	tools := NewTools()

	if !tools.HasBuiltinServer("fetch") {
		t.Error("expected fetch to be a built-in server")
	}
	if tools.HasBuiltinServer("nonexistent") {
		t.Error("nonexistent should not be a built-in server")
	}
}

func TestConnectBuiltinServer(t *testing.T) {
	tl := NewTools()
	ctx := context.Background()

	count, err := tl.ConnectBuiltinServer(ctx, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 tool, got %d", count)
	}

	// Verify tool is registered with prefix.
	schemas := tl.Schema()
	found := false
	for _, s := range schemas {
		if s.Name == "fetch__fetch" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected fetch__fetch tool in schema")
	}

	// BuiltinServerConnected should report true.
	if !tl.BuiltinServerConnected("fetch") {
		t.Error("expected BuiltinServerConnected to be true after connecting")
	}
}

func TestConnectBuiltinServerNotFound(t *testing.T) {
	tl := NewTools()
	_, err := tl.ConnectBuiltinServer(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}
