package mcp

import (
	"os"
	"time"
)

// RegistryEntry describes a well-known MCP server.
type RegistryEntry struct {
	// Name is the short name used in DSL (e.g. "filesystem").
	Name string

	// Description briefly explains what the server provides.
	Description string

	// Command is the executable to run.
	Command string

	// Args are default command-line arguments.
	Args []string

	// RequiredEnv lists environment variables that must be set.
	RequiredEnv []string

	// OptionalEnv lists environment variables that are useful but not required.
	OptionalEnv []string
}

// DefaultRegistry contains well-known MCP servers.
var DefaultRegistry = map[string]RegistryEntry{
	"filesystem": {
		Name:        "filesystem",
		Description: "File system access (read, write, search, list)",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem"},
	},
	"memory": {
		Name:        "memory",
		Description: "Persistent knowledge graph memory",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-memory"},
	},
	"github": {
		Name:        "github",
		Description: "GitHub API access (repos, issues, PRs, files)",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-github"},
		RequiredEnv: []string{"GITHUB_PERSONAL_ACCESS_TOKEN"},
	},
	"brave-search": {
		Name:        "brave-search",
		Description: "Web search via Brave Search API",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-brave-search"},
		RequiredEnv: []string{"BRAVE_API_KEY"},
	},
	"fetch": {
		Name:        "fetch",
		Description: "HTTP fetch for web content retrieval",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-fetch"},
	},
	"postgres": {
		Name:        "postgres",
		Description: "PostgreSQL database access",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-postgres"},
		RequiredEnv: []string{"POSTGRES_CONNECTION_STRING"},
	},
	"sqlite": {
		Name:        "sqlite",
		Description: "SQLite database access",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sqlite"},
	},
	"slack": {
		Name:        "slack",
		Description: "Slack workspace integration",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-slack"},
		RequiredEnv: []string{"SLACK_BOT_TOKEN"},
		OptionalEnv: []string{"SLACK_TEAM_ID"},
	},
	"puppeteer": {
		Name:        "puppeteer",
		Description: "Browser automation via Puppeteer",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-puppeteer"},
	},
	"sequential-thinking": {
		Name:        "sequential-thinking",
		Description: "Dynamic reasoning and thought revision",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
	},
}

// Lookup finds a registry entry by name.
func Lookup(name string) (RegistryEntry, bool) {
	entry, ok := DefaultRegistry[name]
	return entry, ok
}

// ToServerConfig converts a registry entry to a ServerConfig,
// merging any overrides from the caller.
func (e RegistryEntry) ToServerConfig(overrideEnv map[string]string) ServerConfig {
	cfg := ServerConfig{
		Name:      e.Name,
		Transport: TransportStdio,
		Command:   e.Command,
		Args:      append([]string{}, e.Args...),
		Env:       make(map[string]string),
		Timeout:   30 * time.Second,
	}

	// Auto-populate required env from os.Getenv when not overridden.
	for _, key := range e.RequiredEnv {
		if val := os.Getenv(key); val != "" {
			cfg.Env[key] = val
		}
	}

	// Also pull optional env from environment.
	for _, key := range e.OptionalEnv {
		if val := os.Getenv(key); val != "" {
			cfg.Env[key] = val
		}
	}

	// Apply overrides (caller wins).
	for k, v := range overrideEnv {
		cfg.Env[k] = v
	}

	return cfg
}
