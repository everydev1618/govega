package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// NewClient creates a new MCP client.
func NewClient(config ServerConfig) (*Client, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("server name is required")
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	var transport Transport
	switch config.Transport {
	case TransportStdio, "":
		if config.Command == "" {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		transport = NewStdioTransport(config)
	case TransportHTTP, TransportSSE:
		if config.URL == "" {
			return nil, fmt.Errorf("URL is required for HTTP/SSE transport")
		}
		transport = NewHTTPTransport(config)
	default:
		return nil, fmt.Errorf("unknown transport type: %s", config.Transport)
	}

	return &Client{
		name:      config.Name,
		transport: transport,
	}, nil
}

// Connect establishes a connection to the MCP server and performs the handshake.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Connect transport
	if err := c.transport.Connect(ctx); err != nil {
		return fmt.Errorf("connect transport: %w", err)
	}

	// Perform initialization handshake
	initParams := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo: ClientInfo{
			Name:    "vega",
			Version: "1.0.0",
		},
		Capabilities: ClientCapabilities{},
	}

	result, err := c.transport.Send(ctx, "initialize", initParams)
	if err != nil {
		c.transport.Close()
		return fmt.Errorf("initialize: %w", err)
	}

	var initResult InitializeResult
	if err := json.Unmarshal(result, &initResult); err != nil {
		c.transport.Close()
		return fmt.Errorf("parse initialize result: %w", err)
	}

	c.serverInfo = &ServerInfo{
		Name:            initResult.ServerInfo.Name,
		Version:         initResult.ServerInfo.Version,
		ProtocolVersion: initResult.ProtocolVersion,
		Capabilities:    initResult.Capabilities,
	}

	// Send initialized notification
	_, err = c.transport.Send(ctx, "notifications/initialized", nil)
	if err != nil {
		// Some servers don't require this, so don't fail
	}

	// Set up notification handler
	c.transport.OnNotification(c.handleNotification)

	c.connected = true
	return nil
}

// DiscoverTools retrieves the list of tools from the server.
func (c *Client) DiscoverTools(ctx context.Context) ([]MCPTool, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("not connected")
	}
	c.mu.RUnlock()

	result, err := c.transport.Send(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var listResult ToolsListResult
	if err := json.Unmarshal(result, &listResult); err != nil {
		return nil, fmt.Errorf("parse tools list: %w", err)
	}

	// Add server name to each tool
	tools := make([]MCPTool, len(listResult.Tools))
	for i, tool := range listResult.Tools {
		tools[i] = tool
		tools[i].ServerName = c.name
	}

	c.mu.Lock()
	c.tools = tools
	c.mu.Unlock()

	return tools, nil
}

// CallTool executes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return "", fmt.Errorf("not connected")
	}
	c.mu.RUnlock()

	params := ToolCallParams{
		Name:      name,
		Arguments: args,
	}

	result, err := c.transport.Send(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("tools/call: %w", err)
	}

	var callResult ToolCallResult
	if err := json.Unmarshal(result, &callResult); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}

	// Combine content blocks into a single string
	var parts []string
	for _, block := range callResult.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "image":
			parts = append(parts, fmt.Sprintf("[Image: %s]", block.MimeType))
		case "resource":
			parts = append(parts, fmt.Sprintf("[Resource: %s]", block.Text))
		}
	}

	response := strings.Join(parts, "\n")

	if callResult.IsError {
		return "", fmt.Errorf("tool error: %s", response)
	}

	return response, nil
}

// DiscoverResources retrieves the list of resources from the server.
func (c *Client) DiscoverResources(ctx context.Context) ([]MCPResource, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("not connected")
	}
	c.mu.RUnlock()

	result, err := c.transport.Send(ctx, "resources/list", nil)
	if err != nil {
		return nil, fmt.Errorf("resources/list: %w", err)
	}

	var listResult ResourcesListResult
	if err := json.Unmarshal(result, &listResult); err != nil {
		return nil, fmt.Errorf("parse resources list: %w", err)
	}

	c.mu.Lock()
	c.resources = listResult.Resources
	c.mu.Unlock()

	return listResult.Resources, nil
}

// ReadResource reads a resource from the server.
func (c *Client) ReadResource(ctx context.Context, uri string) (string, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return "", fmt.Errorf("not connected")
	}
	c.mu.RUnlock()

	params := ResourceReadParams{
		URI: uri,
	}

	result, err := c.transport.Send(ctx, "resources/read", params)
	if err != nil {
		return "", fmt.Errorf("resources/read: %w", err)
	}

	var readResult ResourceReadResult
	if err := json.Unmarshal(result, &readResult); err != nil {
		return "", fmt.Errorf("parse resource content: %w", err)
	}

	// Return first text content
	for _, content := range readResult.Contents {
		if content.Text != "" {
			return content.Text, nil
		}
	}

	return "", fmt.Errorf("no text content in resource")
}

// Close closes the connection to the server.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false
	return c.transport.Close()
}

// Name returns the server name.
func (c *Client) Name() string {
	return c.name
}

// Connected returns whether the client is connected.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Tools returns the cached tools list.
func (c *Client) Tools() []MCPTool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tools
}

// ServerInfo returns information about the connected server.
func (c *Client) ServerInfo() *ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// handleNotification handles server notifications.
func (c *Client) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "notifications/tools/list_changed":
		// Re-discover tools
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c.DiscoverTools(ctx)

	case "notifications/resources/list_changed":
		// Re-discover resources
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c.DiscoverResources(ctx)
	}
}
