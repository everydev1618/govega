package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fetch tool must present a real browser User-Agent. The old
// "Vega/1.0 (MCP Fetch Server)" UA got 403'd by anti-bot layers (Cloudflare),
// which surfaced to the user as "I got blocked".
func TestFetchToolBrowserUserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	if _, err := fetchToolFunc(context.Background(), map[string]any{"url": server.URL}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if strings.Contains(gotUA, "Vega") || !strings.Contains(gotUA, "Mozilla") {
		t.Errorf("expected a browser-like User-Agent, got %q", gotUA)
	}
}

// A 403 (anti-bot) should produce a clear, honest error the agent can relay,
// not a bare "HTTP 403".
func TestFetchToolBlockedMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := fetchToolFunc(context.Background(), map[string]any{"url": server.URL})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Errorf("403 should yield a 'blocked automated access' error, got %v", err)
	}
}

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

// TestFetchToolContentTypes: images return an honest note (not binary garbage),
// and a PDF content-type routes to extraction (here a fake body → clean error,
// proving we don't dump raw bytes).
func TestFetchToolContentTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pic":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\nbinary"))
		case "/doc":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 not-a-real-pdf"))
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	out, err := fetchToolFunc(ctx, map[string]any{"url": srv.URL + "/pic"})
	if err != nil {
		t.Fatalf("image fetch: %v", err)
	}
	if !strings.Contains(out, "image") || strings.Contains(out, "PNG\r\nbinary") {
		t.Errorf("image URL should return a note, not raw bytes: %q", out)
	}

	_, err = fetchToolFunc(ctx, map[string]any{"url": srv.URL + "/doc"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "pdf") {
		t.Errorf("malformed PDF should yield an honest pdf error, got %v", err)
	}
}
