package vega

import (
	"os"
	"path/filepath"
)

// Home returns the Vega home directory.
// It defaults to ~/.vega but can be overridden with the VEGA_HOME environment variable.
func Home() string {
	if v := os.Getenv("VEGA_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vega")
}

// DefaultDBPath returns the default SQLite database path (~/.vega/vega.db).
func DefaultDBPath() string {
	return filepath.Join(Home(), "vega.db")
}

// WorkspacePath returns the default shared workspace directory.
func WorkspacePath() string {
	return filepath.Join(Home(), "workspace")
}

// EnsureHome creates the Vega home and workspace directories if they don't exist.
func EnsureHome() error {
	return os.MkdirAll(WorkspacePath(), 0o755)
}
