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

// TestExecHeredocPreservesContent covers the deliverable-corruption bug
// (TonyVega pacman, Jul 4): an agent writing a file through an exec heredoc/
// echo must land byte-for-byte on disk. The old rewriteCommandPaths ran a
// blunt path-rewrite regex over the ENTIRE command string, so every "/"-token
// inside the heredoc body — `</canvas>`, `w/2`, `//comments` — got rewritten
// to sandbox/basename, structurally destroying the file before it was written.
func TestExecHeredocPreservesContent(t *testing.T) {
	dir := t.TempDir()
	tools := NewTools(WithSandbox(dir))
	tools.RegisterBuiltins()

	html := "<!DOCTYPE html>\n<canvas id=\"c\"></canvas>\n<script>var x = w/2; // half\nfetch(\"/api/data\");</script>\n</html>"
	// Heredoc with a quoted delimiter so the shell writes the body verbatim.
	cmd := "cat > out.html <<'HTMLEOF'\n" + html + "\nHTMLEOF"

	if _, err := tools.Execute(context.Background(), "exec", map[string]any{"command": cmd}); err != nil {
		t.Fatalf("exec: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != html+"\n" {
		t.Errorf("exec-written content was mangled.\nwant: %q\n got: %q", html+"\n", string(got))
	}
	// Guard the specific corruption signature.
	for _, frag := range []string{"</canvas>", "w/2", "/api/data", "// half"} {
		if !strings.Contains(string(got), frag) {
			t.Errorf("missing/mangled fragment %q in written file", frag)
		}
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
