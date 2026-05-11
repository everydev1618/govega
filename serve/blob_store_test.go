package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFilesystemBlobStore_RoundTrip pins the basic contract: Put then
// Get returns the same bytes, Delete removes them, Get-after-Delete
// is os.ErrNotExist.
func TestFilesystemBlobStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewFilesystemBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("NewFilesystemBlobStore: %v", err)
	}

	content := []byte("hello world")
	if err := bs.Put("brain_abc", content); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := bs.Get("brain_abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("got %q, want %q", got, content)
	}

	if err := bs.Delete("brain_abc"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if _, err := bs.Get("brain_abc"); !os.IsNotExist(err) {
		t.Errorf("Get after delete err = %v, want IsNotExist", err)
	}

	// Idempotent delete.
	if err := bs.Delete("brain_abc"); err != nil {
		t.Errorf("Delete (idempotent) returned %v", err)
	}
}

// TestFilesystemBlobStore_RejectsPathTraversal makes sure a malicious
// key can't escape the base directory. filepath.Clean handles ".."
// segments; we verify by attempting one and confirming the result
// stays under dir.
func TestFilesystemBlobStore_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "blobs")
	bs, err := NewFilesystemBlobStore(base)
	if err != nil {
		t.Fatalf("NewFilesystemBlobStore: %v", err)
	}
	if err := bs.Put("../../etc/passwd", []byte("oops")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// The clean key resolves to /etc/passwd → joined with base, lands
	// under base/etc/passwd. Confirm it stayed inside base.
	if _, err := os.Stat(filepath.Join(base, "etc", "passwd")); err != nil {
		t.Errorf("expected file under base, got %v", err)
	}
	// And nothing exists at the real /etc/passwd-relative path (the
	// machine's actual /etc/passwd is untouched, but proving that
	// non-destructively just means: nothing wrote outside base).
	outside := filepath.Join(dir, "etc", "passwd")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Errorf("file leaked outside base: %v", outside)
	}
}

// TestFilesystemBlobStore_RequiresDir guards the "empty path" footgun.
func TestFilesystemBlobStore_RequiresDir(t *testing.T) {
	if _, err := NewFilesystemBlobStore(""); err == nil {
		t.Error("expected error for empty dir")
	}
}
