package serve

import (
	"fmt"
	"os"
	"path/filepath"
)

// BlobStore is the abstract object-storage surface for content that
// shouldn't live in the relational database — today, agent brain files
// (govega#43), eventually chat attachments (#48) and anything else that
// outgrows BLOB / BYTEA inline storage (refs govega#61 phase 5).
//
// The interface is deliberately narrow: opaque key, bytes in, bytes out.
// Keys are caller-chosen (the brain handler uses each file's `brain_<hex>`
// id) so this interface doesn't need to invent its own naming scheme.
type BlobStore interface {
	// Put writes content under key, overwriting any existing value.
	Put(key string, content []byte) error
	// Get returns the content stored at key, or os.ErrNotExist when
	// the key has no value.
	Get(key string) ([]byte, error)
	// Delete removes the value at key. Returns nil whether the key
	// existed or not — idempotent.
	Delete(key string) error
}

// FilesystemBlobStore writes blobs as files under a base directory.
// Used for dev / single-host installs. Keys are passed through
// filepath.Clean and rejected if they would escape the base — same
// protection the brain handler already does on filenames, applied
// belt-and-braces here so this layer is safe on its own.
type FilesystemBlobStore struct {
	dir string
}

// NewFilesystemBlobStore returns a store rooted at dir. The directory
// is created with 0700 if missing.
func NewFilesystemBlobStore(dir string) (*FilesystemBlobStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("blob store dir is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create blob dir %q: %w", dir, err)
	}
	return &FilesystemBlobStore{dir: dir}, nil
}

func (fs *FilesystemBlobStore) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if clean == "/" {
		return "", fmt.Errorf("blob key is empty")
	}
	return filepath.Join(fs.dir, clean), nil
}

func (fs *FilesystemBlobStore) Put(key string, content []byte) error {
	p, err := fs.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("mkdir for blob %q: %w", key, err)
	}
	return os.WriteFile(p, content, 0600)
}

func (fs *FilesystemBlobStore) Get(key string) ([]byte, error) {
	p, err := fs.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (fs *FilesystemBlobStore) Delete(key string) error {
	p, err := fs.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Compile-time assertion.
var _ BlobStore = (*FilesystemBlobStore)(nil)
