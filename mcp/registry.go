package mcp

import (
	"fmt"
	"os"
	"time"
)

// RegistryEntry describes a well-known MCP server.
type RegistryEntry struct {
	// Name is the short name used in DSL (e.g. "filesystem").
	Name string

	// Description briefly explains what the server provides.
	Description string

	// Transport is the transport type (stdio, http, sse). Defaults to stdio.
	Transport TransportType

	// Command is the executable to run (stdio transport).
	Command string

	// Args are default command-line arguments (stdio transport).
	Args []string

	// URL is the server endpoint (http/sse transport).
	URL string

	// Headers are default HTTP headers (http/sse transport).
	Headers map[string]string

	// RequiredEnv lists environment variables that must be set.
	RequiredEnv []string

	// OptionalEnv lists environment variables that are useful but not required.
	OptionalEnv []string

	// BuiltinGo indicates this server has a native Go implementation
	// that runs in-process without requiring Node.js or any external binary.
	BuiltinGo bool

	// GitHubRepo is the "owner/repo" for auto-downloading release binaries.
	GitHubRepo string

	// Icon is a Lucide icon name surfaced on the FE's integrations grid
	// (refs govega#44). Pairs with Category so apex-host-mgmt's tool
	// picker can render each integration with a recognizable badge.
	Icon string

	// Category groups integrations for the FE's tools tab. Suggested
	// taxonomy: communication, dev_tools, crm, project_mgmt, cloud,
	// data, productivity, web. Empty string is treated as
	// "uncategorized" by the FE.
	Category string
}

// DefaultRegistry contains well-known MCP servers. Icon names match the
// FE's Lucide bundle (lucide-react). Category values match the taxonomy
// agreed for the apex-host-mgmt tools tab (refs govega#44).
var DefaultRegistry = map[string]RegistryEntry{
	"filesystem": {
		Name:        "filesystem",
		Description: "File system access (read, write, search, list)",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem"},
		Icon:        "FolderOpen",
		Category:    "data",
	},
	"memory": {
		Name:        "memory",
		Description: "Persistent knowledge graph memory",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-memory"},
		Icon:        "Brain",
		Category:    "productivity",
	},
	"github": {
		Name:        "github",
		Description: "GitHub API access (repos, issues, PRs, files)",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-github"},
		RequiredEnv: []string{"GITHUB_PERSONAL_ACCESS_TOKEN"},
		Icon:        "Github",
		Category:    "dev_tools",
	},
	"brave-search": {
		Name:        "brave-search",
		Description: "Web search via Brave Search API",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-brave-search"},
		RequiredEnv: []string{"BRAVE_API_KEY"},
		Icon:        "Search",
		Category:    "web",
	},
	"fetch": {
		Name:        "fetch",
		Description: "HTTP fetch for web content retrieval (native Go — no Node.js required)",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-fetch"},
		BuiltinGo:   true,
		Icon:        "Globe",
		Category:    "web",
	},
	"postgres": {
		Name:        "postgres",
		Description: "PostgreSQL database access",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-postgres"},
		RequiredEnv: []string{"POSTGRES_CONNECTION_STRING"},
		Icon:        "Database",
		Category:    "data",
	},
	"sqlite": {
		Name:        "sqlite",
		Description: "SQLite database access",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sqlite"},
		Icon:        "Database",
		Category:    "data",
	},
	"slack": {
		Name:        "slack",
		Description: "Slack workspace integration",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-slack"},
		RequiredEnv: []string{"SLACK_BOT_TOKEN"},
		OptionalEnv: []string{"SLACK_TEAM_ID"},
		Icon:        "MessageSquare",
		Category:    "communication",
	},
	"puppeteer": {
		Name:        "puppeteer",
		Description: "Browser automation via Puppeteer",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-puppeteer"},
		Icon:        "MousePointer",
		Category:    "web",
	},
	"mssql": {
		Name:        "mssql",
		Description: "Microsoft SQL Server database access (native Go — no Node.js required)",
		Command:     "npx",
		Args:        []string{"-y", "@connorbritain/mssql-mcp-server@latest"},
		RequiredEnv: []string{"SERVER_NAME", "DATABASE_NAME", "SQL_USERNAME", "SQL_PASSWORD"},
		OptionalEnv: []string{"SQL_PORT", "SQL_AUTH_MODE", "TRUST_SERVER_CERTIFICATE"},
		BuiltinGo:   true,
		Icon:        "Database",
		Category:    "data",
	},
	"sequential-thinking": {
		Name:        "sequential-thinking",
		Description: "Dynamic reasoning and thought revision",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
		Icon:        "Workflow",
		Category:    "productivity",
	},
	"composio": {
		Name:        "composio",
		Description: "Composio integration platform (850+ app integrations with managed auth)",
		Transport:   TransportHTTP,
		URL:         "https://connect.composio.dev/mcp",
		RequiredEnv: []string{"COMPOSIO_API_KEY"},
		Icon:        "Plug",
		Category:    "productivity",
	},
	"gmail": {
		Name:        "gmail",
		Description: "Gmail API (list, read, label, archive, draft) — built-in Go server using a BYO OAuth refresh token",
		BuiltinGo:   true,
		RequiredEnv: []string{"GMAIL_CLIENT_ID", "GMAIL_CLIENT_SECRET", "GMAIL_REFRESH_TOKEN"},
		Icon:        "Mail",
		Category:    "communication",
	},
}

// Lookup finds a registry entry by name.
func Lookup(name string) (RegistryEntry, bool) {
	entry, ok := DefaultRegistry[name]
	return entry, ok
}

// Register adds an entry to the registry. Embedding products call this
// at startup to register product-specific MCP servers (e.g. Vapi) so
// they show up alongside govega's built-in entries in list_mcp_registry.
//
// Returns an error if a different entry already exists under that name.
// Re-registering an identical entry is a no-op (safe to call from init
// or main multiple times).
func Register(entry RegistryEntry) error {
	if entry.Name == "" {
		return fmt.Errorf("mcp.Register: entry.Name is empty")
	}
	if existing, ok := DefaultRegistry[entry.Name]; ok {
		if entriesEqual(existing, entry) {
			return nil
		}
		return fmt.Errorf("mcp.Register: %q already registered with different shape", entry.Name)
	}
	DefaultRegistry[entry.Name] = entry
	return nil
}

func entriesEqual(a, b RegistryEntry) bool {
	if a.Name != b.Name || a.Description != b.Description ||
		a.Transport != b.Transport || a.Command != b.Command ||
		a.URL != b.URL || a.BuiltinGo != b.BuiltinGo ||
		a.GitHubRepo != b.GitHubRepo ||
		a.Icon != b.Icon || a.Category != b.Category {
		return false
	}
	if !stringSliceEq(a.Args, b.Args) || !stringSliceEq(a.RequiredEnv, b.RequiredEnv) ||
		!stringSliceEq(a.OptionalEnv, b.OptionalEnv) {
		return false
	}
	if len(a.Headers) != len(b.Headers) {
		return false
	}
	for k, v := range a.Headers {
		if b.Headers[k] != v {
			return false
		}
	}
	return true
}

func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ToServerConfig converts a registry entry to a ServerConfig,
// merging any overrides from the caller.
func (e RegistryEntry) ToServerConfig(overrideEnv map[string]string) ServerConfig {
	transport := e.Transport
	if transport == "" {
		transport = TransportStdio
	}

	cfg := ServerConfig{
		Name:      e.Name,
		Transport: transport,
		Command:   e.Command,
		Args:      append([]string{}, e.Args...),
		URL:       e.URL,
		Env:       make(map[string]string),
		Timeout:   30 * time.Second,
	}

	// Copy default headers.
	if len(e.Headers) > 0 {
		cfg.Headers = make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			cfg.Headers[k] = v
		}
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

	// Inject env-based headers for HTTP servers (e.g. API keys).
	if transport == TransportHTTP || transport == TransportSSE {
		if cfg.Headers == nil {
			cfg.Headers = make(map[string]string)
		}
		if apiKey := cfg.Env["COMPOSIO_API_KEY"]; apiKey != "" && e.Name == "composio" {
			cfg.Headers["x-api-key"] = apiKey
		}
	}

	cfg.GitHubRepo = e.GitHubRepo

	return cfg
}
