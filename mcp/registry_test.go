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
		{"composio", true},
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
		if entry.Command == "" && entry.URL == "" {
			t.Errorf("registry entry %q has neither Command nor URL", name)
		}
		if entry.Description == "" {
			t.Errorf("registry entry %q has empty Description", name)
		}
	}
}

func TestComposioRegistryEntry(t *testing.T) {
	entry, ok := Lookup("composio")
	if !ok {
		t.Fatal("composio not in registry")
	}

	if entry.Transport != TransportHTTP {
		t.Errorf("Transport=%q, want %q", entry.Transport, TransportHTTP)
	}
	if entry.URL == "" {
		t.Error("URL should not be empty")
	}

	// Test ToServerConfig produces HTTP config with headers
	os.Setenv("COMPOSIO_API_KEY", "test-key")
	defer os.Unsetenv("COMPOSIO_API_KEY")

	cfg := entry.ToServerConfig(nil)

	if cfg.Transport != TransportHTTP {
		t.Errorf("config Transport=%q, want %q", cfg.Transport, TransportHTTP)
	}
	if cfg.URL == "" {
		t.Error("config URL should not be empty")
	}
	if cfg.Headers["x-api-key"] != "test-key" {
		t.Errorf("config Headers[x-api-key]=%q, want %q", cfg.Headers["x-api-key"], "test-key")
	}
}

func TestHTTPRegistryEntryToServerConfig(t *testing.T) {
	entry := RegistryEntry{
		Name:        "test-http",
		Description: "Test HTTP server",
		Transport:   TransportHTTP,
		URL:         "https://example.com/mcp",
		Headers:     map[string]string{"Authorization": "Bearer token"},
		RequiredEnv: []string{"TEST_KEY"},
	}

	os.Setenv("TEST_KEY", "val")
	defer os.Unsetenv("TEST_KEY")

	cfg := entry.ToServerConfig(nil)

	if cfg.Transport != TransportHTTP {
		t.Errorf("Transport=%q, want %q", cfg.Transport, TransportHTTP)
	}
	if cfg.URL != "https://example.com/mcp" {
		t.Errorf("URL=%q", cfg.URL)
	}
	if cfg.Headers["Authorization"] != "Bearer token" {
		t.Errorf("Headers not copied")
	}
	if cfg.Command != "" {
		t.Errorf("Command should be empty for HTTP, got %q", cfg.Command)
	}
}
