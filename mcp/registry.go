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
		Description: "HTTP fetch for web content retrieval (native Go — no Node.js required)",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-fetch"},
		BuiltinGo:   true,
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
	"mssql": {
		Name:        "mssql",
		Description: "Microsoft SQL Server database access (native Go — no Node.js required)",
		Command:     "npx",
		Args:        []string{"-y", "@connorbritain/mssql-mcp-server@latest"},
		RequiredEnv: []string{"SERVER_NAME", "DATABASE_NAME", "SQL_USERNAME", "SQL_PASSWORD"},
		OptionalEnv: []string{"SQL_PORT", "SQL_AUTH_MODE", "TRUST_SERVER_CERTIFICATE"},
		BuiltinGo:   true,
	},
	"sequential-thinking": {
		Name:        "sequential-thinking",
		Description: "Dynamic reasoning and thought revision",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
	},
	"synkedup": {
		Name:        "synkedup",
		Description: "SynkedUp landscape business management (customers, projects, calendar, items, users)",
		Command:     "synkedup-vega-mcp",
		RequiredEnv: []string{"SYNKEDUP_API_URL", "SYNKEDUP_USERNAME", "SYNKEDUP_PASSWORD"},
		GitHubRepo:  "etdebruin/synkedup-vega-mcp",
	},
	"composio": {
		Name:        "composio",
		Description: "Composio integration platform (850+ app integrations with managed auth)",
		Transport:   TransportHTTP,
		URL:         "https://mcp.composio.dev/v2/mcp",
		RequiredEnv: []string{"COMPOSIO_API_KEY"},
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
