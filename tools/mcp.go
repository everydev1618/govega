package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/everydev1618/govega/mcp"
)

// mcpClientEntry holds an MCP client with its configuration.
type mcpClientEntry struct {
	client *mcp.Client
	config mcp.ServerConfig
}

// WithMCPServer adds an MCP server to the tools collection.
// Tools from the server will be prefixed with "server_name__tool_name".
func WithMCPServer(config mcp.ServerConfig) ToolsOption {
	return func(t *Tools) {
		if t.mcpClients == nil {
			t.mcpClients = make([]*mcpClientEntry, 0)
		}

		client, err := mcp.NewClient(config)
		if err != nil {
			slog.Warn("mcp: failed to create client", "server", config.Name, "error", err)
			return
		}

		t.mcpClients = append(t.mcpClients, &mcpClientEntry{
			client: client,
			config: config,
		})
	}
}

// ConnectMCP connects all MCP servers and discovers their tools.
func (t *Tools) ConnectMCP(ctx context.Context) error {
	t.mu.Lock()
	clients := t.mcpClients
	t.mu.Unlock()

	var connected int
	for _, entry := range clients {
		if err := entry.client.Connect(ctx); err != nil {
			slog.Warn("mcp: failed to connect server", "server", entry.config.Name, "error", err)
			continue
		}

		mcpTools, err := entry.client.DiscoverTools(ctx)
		if err != nil {
			slog.Warn("mcp: failed to discover tools", "server", entry.config.Name, "error", err)
			continue
		}

		// Register each MCP tool as a tool
		for _, mcpTool := range mcpTools {
			t.registerMCPTool(entry.client, mcpTool)
		}
		connected++
		slog.Info("mcp: connected server", "server", entry.config.Name, "tools", len(mcpTools))
	}

	// Register the global mcp_read_resource tool if any servers connected.
	if connected > 0 {
		t.registerMCPReadResourceTool()
	}

	return nil
}

// registerMCPReadResourceTool registers a tool that reads resources from any connected MCP server.
func (t *Tools) registerMCPReadResourceTool() {
	// Don't register if already exists.
	t.mu.RLock()
	_, exists := t.tools["mcp_read_resource"]
	t.mu.RUnlock()
	if exists {
		return
	}

	fn := func(ctx context.Context, params map[string]any) (string, error) {
		server, _ := params["server"].(string)
		uri, _ := params["uri"].(string)
		if server == "" || uri == "" {
			return "", fmt.Errorf("both 'server' and 'uri' parameters are required")
		}
		return t.ReadMCPResource(ctx, server, uri)
	}

	t.Register("mcp_read_resource", ToolDef{
		Description: "Read a resource from a connected MCP server",
		Fn:          fn,
		Params: map[string]ParamDef{
			"server": {Type: "string", Description: "MCP server name", Required: true},
			"uri":    {Type: "string", Description: "Resource URI to read", Required: true},
		},
	})
}

// ConnectMCPServer connects a single MCP server by config at runtime,
// discovers its tools, and registers them. Returns the number of tools found.
func (t *Tools) ConnectMCPServer(ctx context.Context, config mcp.ServerConfig) (int, error) {
	client, err := mcp.NewClient(config)
	if err != nil {
		return 0, fmt.Errorf("create MCP client %s: %w", config.Name, err)
	}

	if err := client.Connect(ctx); err != nil {
		return 0, fmt.Errorf("connect MCP server %s: %w", config.Name, err)
	}

	mcpTools, err := client.DiscoverTools(ctx)
	if err != nil {
		client.Close()
		return 0, fmt.Errorf("discover tools from %s: %w", config.Name, err)
	}

	entry := &mcpClientEntry{client: client, config: config}
	t.mu.Lock()
	if t.mcpClients == nil {
		t.mcpClients = make([]*mcpClientEntry, 0)
	}
	t.mcpClients = append(t.mcpClients, entry)
	t.mu.Unlock()

	for _, mcpTool := range mcpTools {
		t.registerMCPTool(client, mcpTool)
	}

	// Ensure the global resource tool exists.
	t.registerMCPReadResourceTool()

	slog.Info("mcp: connected server at runtime", "server", config.Name, "tools", len(mcpTools))
	return len(mcpTools), nil
}

// MCPServerConnected reports whether a server with the given name is already connected.
func (t *Tools) MCPServerConnected(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, entry := range t.mcpClients {
		if entry.config.Name == name && entry.client.Connected() {
			return true
		}
	}
	return false
}

// DisconnectMCP disconnects all MCP servers.
func (t *Tools) DisconnectMCP() error {
	t.mu.Lock()
	clients := t.mcpClients
	t.mu.Unlock()

	var lastErr error
	for _, entry := range clients {
		if err := entry.client.Close(); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// registerMCPTool registers a single MCP tool as a tool.
func (t *Tools) registerMCPTool(client *mcp.Client, mcpTool mcp.MCPTool) {
	// Create prefixed name: server__toolname
	name := client.Name() + "__" + mcpTool.Name

	// Build params from input schema
	params := extractParamsFromSchema(mcpTool.InputSchema)

	// Create executor that calls the MCP tool
	fn := func(ctx context.Context, args map[string]any) (string, error) {
		return client.CallTool(ctx, mcpTool.Name, args)
	}

	t.Register(name, ToolDef{
		Description: mcpTool.Description,
		Fn:          fn,
		Params:      params,
	})
}

// extractParamsFromSchema converts JSON Schema to ParamDef map.
func extractParamsFromSchema(schema map[string]any) map[string]ParamDef {
	params := make(map[string]ParamDef)

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return params
	}

	// Get required fields
	required := make(map[string]bool)
	if reqList, ok := schema["required"].([]any); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	for name, propRaw := range properties {
		prop, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}

		param := ParamDef{
			Required: required[name],
		}

		if tp, ok := prop["type"].(string); ok {
			param.Type = tp
		} else {
			param.Type = "string"
		}

		if d, ok := prop["description"].(string); ok {
			param.Description = d
		}

		if def := prop["default"]; def != nil {
			param.Default = def
		}

		if enum, ok := prop["enum"].([]any); ok {
			for _, e := range enum {
				if s, ok := e.(string); ok {
					param.Enum = append(param.Enum, s)
				}
			}
		}

		params[name] = param
	}

	return params
}

// ReadMCPResource reads a resource from a specific MCP server by name.
func (t *Tools) ReadMCPResource(ctx context.Context, serverName, uri string) (string, error) {
	t.mu.RLock()
	clients := t.mcpClients
	t.mu.RUnlock()

	for _, entry := range clients {
		if entry.config.Name == serverName {
			if !entry.client.Connected() {
				return "", fmt.Errorf("MCP server %q not connected", serverName)
			}
			return entry.client.ReadResource(ctx, uri)
		}
	}

	return "", fmt.Errorf("MCP server %q not found", serverName)
}

// FilterMCP returns a new Tools with only tools from specified MCP servers.
// Supports patterns like "server__*" to include all tools from a server.
func (t *Tools) FilterMCP(patterns ...string) *Tools {
	t.mu.RLock()
	defer t.mu.RUnlock()

	filtered := &Tools{
		tools:      make(map[string]*tool),
		middleware: t.middleware,
		sandbox:    t.sandbox,
		mcpClients: t.mcpClients,
	}

	for name, tl := range t.tools {
		for _, pattern := range patterns {
			if matchToolPattern(name, pattern) {
				filtered.tools[name] = tl
				break
			}
		}
	}

	return filtered
}

// matchToolPattern checks if a tool name matches a pattern.
// Supports:
// - Exact match: "server__tool"
// - Prefix wildcard: "server__*"
// - Suffix wildcard: "*__tool"
func matchToolPattern(name, pattern string) bool {
	if pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(name, prefix)
	}

	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(name, suffix)
	}

	return name == pattern
}

// MCPServerStatus describes the status of a connected MCP server.
type MCPServerStatus struct {
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Transport string   `json:"transport,omitempty"`
	URL       string   `json:"url,omitempty"`
	Command   string   `json:"command,omitempty"`
	Tools     []string `json:"tools"`
}

// MCPServerStatuses returns the status of all configured MCP servers.
func (t *Tools) MCPServerStatuses() []MCPServerStatus {
	t.mu.RLock()
	clients := t.mcpClients
	t.mu.RUnlock()

	statuses := make([]MCPServerStatus, 0, len(clients))
	for _, entry := range clients {
		s := MCPServerStatus{
			Name:      entry.config.Name,
			Connected: entry.client.Connected(),
			Transport: string(entry.config.Transport),
			URL:       entry.config.URL,
			Command:   entry.config.Command,
		}
		for _, mcpTool := range entry.client.Tools() {
			s.Tools = append(s.Tools, mcpTool.Name)
		}
		statuses = append(statuses, s)
	}
	return statuses
}

// MCPServerOption configures MCP server behavior.
type MCPServerOption func(*mcpServerOptions)

type mcpServerOptions struct {
	timeout     time.Duration
	autoConnect bool
}

// WithMCPTimeout sets the timeout for MCP operations.
func WithMCPTimeout(d time.Duration) MCPServerOption {
	return func(o *mcpServerOptions) {
		o.timeout = d
	}
}

// WithMCPAutoConnect enables automatic connection on first tool call.
func WithMCPAutoConnect(enabled bool) MCPServerOption {
	return func(o *mcpServerOptions) {
		o.autoConnect = enabled
	}
}
