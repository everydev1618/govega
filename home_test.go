package vega

import (
	"os"
	"strings"
	"testing"
)

func TestHome_Default(t *testing.T) {
	// Clear env to test default
	orig := os.Getenv("VEGA_HOME")
	os.Unsetenv("VEGA_HOME")
	defer func() {
		if orig != "" {
			os.Setenv("VEGA_HOME", orig)
		}
	}()

	home := Home()
	if !strings.HasSuffix(home, ".vega") {
		t.Errorf("Home() = %q, should end with .vega", home)
	}
}

func TestHome_EnvOverride(t *testing.T) {
	orig := os.Getenv("VEGA_HOME")
	os.Setenv("VEGA_HOME", "/custom/vega/home")
	defer func() {
		if orig != "" {
			os.Setenv("VEGA_HOME", orig)
		} else {
			os.Unsetenv("VEGA_HOME")
		}
	}()

	home := Home()
	if home != "/custom/vega/home" {
		t.Errorf("Home() = %q, want %q", home, "/custom/vega/home")
	}
}

func TestDefaultDBPath(t *testing.T) {
	path := DefaultDBPath()
	if !strings.HasSuffix(path, "vega.db") {
		t.Errorf("DefaultDBPath() = %q, should end with vega.db", path)
	}
}

func TestWorkspacePath(t *testing.T) {
	path := WorkspacePath()
	if !strings.HasSuffix(path, "workspace") {
		t.Errorf("WorkspacePath() = %q, should end with workspace", path)
	}
}

func TestBinPath(t *testing.T) {
	path := BinPath()
	if !strings.HasSuffix(path, "bin") {
		t.Errorf("BinPath() = %q, should end with bin", path)
	}
}

func TestEnsureHome(t *testing.T) {
	dir := t.TempDir()

	orig := os.Getenv("VEGA_HOME")
	os.Setenv("VEGA_HOME", dir)
	defer func() {
		if orig != "" {
			os.Setenv("VEGA_HOME", orig)
		} else {
			os.Unsetenv("VEGA_HOME")
		}
	}()

	err := EnsureHome()
	if err != nil {
		t.Fatalf("EnsureHome() error: %v", err)
	}

	// Workspace directory should exist
	wsPath := WorkspacePath()
	info, err := os.Stat(wsPath)
	if err != nil {
		t.Fatalf("Workspace dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Workspace path should be a directory")
	}
}
