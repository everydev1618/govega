package mcp

import (
	"os"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name  string
		found bool
	}{
		{"filesystem", true},
		{"github", true},
		{"brave-search", true},
		{"fetch", true},
		{"postgres", true},
		{"sqlite", true},
		{"memory", true},
		{"slack", true},
		{"puppeteer", true},
		{"sequential-thinking", true},
		{"nonexistent", false},
	}

	for _, tt := range tests {
		entry, ok := Lookup(tt.name)
		if ok != tt.found {
			t.Errorf("Lookup(%q): got found=%v, want %v", tt.name, ok, tt.found)
		}
		if ok && entry.Name != tt.name {
			t.Errorf("Lookup(%q): entry.Name=%q, want %q", tt.name, entry.Name, tt.name)
		}
	}
}

func TestRegistryEntryToServerConfig(t *testing.T) {
	entry, ok := Lookup("filesystem")
	if !ok {
		t.Fatal("filesystem not in registry")
	}

	cfg := entry.ToServerConfig(nil)

	if cfg.Name != "filesystem" {
		t.Errorf("Name=%q, want %q", cfg.Name, "filesystem")
	}
	if cfg.Transport != TransportStdio {
		t.Errorf("Transport=%q, want %q", cfg.Transport, TransportStdio)
	}
	if cfg.Command != "npx" {
		t.Errorf("Command=%q, want %q", cfg.Command, "npx")
	}
	if len(cfg.Args) < 2 {
		t.Errorf("Args=%v, expected at least 2 args", cfg.Args)
	}
}

func TestToServerConfigEnvOverride(t *testing.T) {
	entry, _ := Lookup("github")

	// Set env for auto-populate
	os.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "auto-token")
	defer os.Unsetenv("GITHUB_PERSONAL_ACCESS_TOKEN")

	// Override env should win
	cfg := entry.ToServerConfig(map[string]string{
		"GITHUB_PERSONAL_ACCESS_TOKEN": "override-token",
	})

	if cfg.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "override-token" {
		t.Errorf("env override not applied: got %q", cfg.Env["GITHUB_PERSONAL_ACCESS_TOKEN"])
	}
}

func TestToServerConfigAutoPopulateEnv(t *testing.T) {
	entry, _ := Lookup("github")

	os.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "from-env")
	defer os.Unsetenv("GITHUB_PERSONAL_ACCESS_TOKEN")

	cfg := entry.ToServerConfig(nil)

	if cfg.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "from-env" {
		t.Errorf("env auto-populate failed: got %q", cfg.Env["GITHUB_PERSONAL_ACCESS_TOKEN"])
	}
}

func TestToServerConfigArgsCopy(t *testing.T) {
	entry, _ := Lookup("filesystem")

	cfg1 := entry.ToServerConfig(nil)
	cfg2 := entry.ToServerConfig(nil)

	// Mutating one config's args shouldn't affect the other.
	if len(cfg1.Args) > 0 {
		cfg1.Args[0] = "mutated"
		if cfg2.Args[0] == "mutated" {
			t.Error("Args slice is shared between configs")
		}
	}
}

func TestDefaultRegistryComplete(t *testing.T) {
	for name, entry := range DefaultRegistry {
		if entry.Name != name {
			t.Errorf("registry key %q != entry.Name %q", name, entry.Name)
		}
		if entry.Command == "" {
			t.Errorf("registry entry %q has empty Command", name)
		}
		if entry.Description == "" {
			t.Errorf("registry entry %q has empty Description", name)
		}
	}
}
