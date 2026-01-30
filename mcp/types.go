// Package mcp provides a client for the Model Context Protocol.
package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// TransportType identifies the transport mechanism.
type TransportType string

const (
	// TransportStdio uses subprocess stdin/stdout.
	TransportStdio TransportType = "stdio"
	// TransportHTTP uses HTTP with SSE for notifications.
	TransportHTTP TransportType = "http"
	// TransportSSE uses Server-Sent Events.
	TransportSSE TransportType = "sse"
)

// Transport is the interface for MCP communication.
type Transport interface {
	// Connect establishes the connection.
	Connect(ctx context.Context) error

	// Send sends a JSON-RPC request and returns the result.
	Send(ctx context.Context, method string, params any) (json.RawMessage, error)

	// Close closes the connection.
	Close() error

	// OnNotification registers a handler for server notifications.
	OnNotification(handler func(method string, params json.RawMessage))
}

// Client is an MCP client that can connect to MCP servers.
type Client struct {
	name      string
	transport Transport
	tools     []MCPTool
	resources []MCPResource
	connected bool
	serverInfo *ServerInfo
	mu        sync.RWMutex
}

// MCPTool represents a tool provided by an MCP server.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	ServerName  string         `json:"-"` // Set by client
}

// MCPResource represents a resource provided by an MCP server.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ServerConfig configures an MCP server connection.
type ServerConfig struct {
	// Name is a human-readable identifier for the server.
	Name string

	// Transport specifies the transport type.
	Transport TransportType

	// Command is the executable for stdio transport.
	Command string

	// Args are command-line arguments for stdio transport.
	Args []string

	// Env are environment variables for stdio transport.
	Env map[string]string

	// URL is the endpoint for HTTP/SSE transport.
	URL string

	// Headers are HTTP headers for HTTP/SSE transport.
	Headers map[string]string

	// Timeout for operations (default: 30s).
	Timeout time.Duration
}

// ServerInfo contains information about the connected MCP server.
type ServerInfo struct {
	Name            string       `json:"name"`
	Version         string       `json:"version"`
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
}

// Capabilities describes what the server supports.
type Capabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

// ToolsCapability indicates tool support.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability indicates resource support.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability indicates prompt support.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// JSON-RPC types

// JSONRPCRequest is a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError is a JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return e.Message
}

// JSONRPCNotification is a JSON-RPC 2.0 notification.
type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// MCP protocol types

// InitializeParams are the parameters for the initialize request.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      ClientInfo `json:"clientInfo"`
	Capabilities    ClientCapabilities `json:"capabilities"`
}

// ClientInfo describes the client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities describes what the client supports.
type ClientCapabilities struct {
	Roots    *RootsCapability    `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

// RootsCapability indicates root support.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCapability indicates sampling support.
type SamplingCapability struct{}

// InitializeResult is the result of the initialize request.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Capabilities    Capabilities `json:"capabilities"`
}

// ToolsListResult is the result of tools/list.
type ToolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

// ToolCallParams are the parameters for tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallResult is the result of tools/call.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is a content block in a tool result.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // Base64 for binary
}

// ResourcesListResult is the result of resources/list.
type ResourcesListResult struct {
	Resources []MCPResource `json:"resources"`
}

// ResourceReadParams are the parameters for resources/read.
type ResourceReadParams struct {
	URI string `json:"uri"`
}

// ResourceReadResult is the result of resources/read.
type ResourceReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ResourceContent is the content of a resource.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // Base64
}

// Error codes
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// Protocol version
const ProtocolVersion = "2024-11-05"
