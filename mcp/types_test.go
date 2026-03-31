package mcp

import (
	"encoding/json"
	"testing"
)

func TestTransportTypes(t *testing.T) {
	tests := []struct {
		tt   TransportType
		want string
	}{
		{TransportStdio, "stdio"},
		{TransportHTTP, "http"},
		{TransportSSE, "sse"},
	}

	for _, tt := range tests {
		if string(tt.tt) != tt.want {
			t.Errorf("TransportType = %q, want %q", tt.tt, tt.want)
		}
	}
}

func TestJSONRPCError_Error(t *testing.T) {
	err := &JSONRPCError{
		Code:    ErrCodeMethodNotFound,
		Message: "method not found",
	}

	if err.Error() != "method not found" {
		t.Errorf("Error() = %q, want %q", err.Error(), "method not found")
	}
}

func TestErrorCodes(t *testing.T) {
	tests := []struct {
		code int
		name string
	}{
		{ErrCodeParse, "Parse"},
		{ErrCodeInvalidRequest, "InvalidRequest"},
		{ErrCodeMethodNotFound, "MethodNotFound"},
		{ErrCodeInvalidParams, "InvalidParams"},
		{ErrCodeInternal, "Internal"},
	}

	for _, tt := range tests {
		if tt.code >= 0 {
			t.Errorf("%s code = %d, should be negative", tt.name, tt.code)
		}
	}
}

func TestProtocolVersion(t *testing.T) {
	if ProtocolVersion == "" {
		t.Error("ProtocolVersion should not be empty")
	}
}

func TestMCPToolJSON(t *testing.T) {
	tool := MCPTool{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path",
				},
			},
		},
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var loaded MCPTool
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if loaded.Name != "read_file" {
		t.Errorf("Name = %q, want %q", loaded.Name, "read_file")
	}
}

func TestMCPResourceJSON(t *testing.T) {
	resource := MCPResource{
		URI:         "file:///tmp/test.txt",
		Name:        "test.txt",
		Description: "A test file",
		MimeType:    "text/plain",
	}

	data, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var loaded MCPResource
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if loaded.URI != "file:///tmp/test.txt" {
		t.Errorf("URI = %q, want %q", loaded.URI, "file:///tmp/test.txt")
	}
	if loaded.MimeType != "text/plain" {
		t.Errorf("MimeType = %q, want %q", loaded.MimeType, "text/plain")
	}
}

func TestServerConfig(t *testing.T) {
	config := ServerConfig{
		Name:      "test-server",
		Transport: TransportStdio,
		Command:   "/usr/local/bin/mcp-server",
		Args:      []string{"--port", "8080"},
		Env:       map[string]string{"API_KEY": "test123"},
	}

	if config.Name != "test-server" {
		t.Errorf("Name = %q, want %q", config.Name, "test-server")
	}
	if config.Transport != TransportStdio {
		t.Errorf("Transport = %q, want %q", config.Transport, TransportStdio)
	}
}

func TestServerInfo(t *testing.T) {
	info := ServerInfo{
		Name:            "test-server",
		Version:         "1.0.0",
		ProtocolVersion: ProtocolVersion,
		Capabilities: Capabilities{
			Tools:     &ToolsCapability{ListChanged: true},
			Resources: &ResourcesCapability{Subscribe: true},
		},
	}

	if info.Capabilities.Tools == nil {
		t.Error("Tools capability should not be nil")
	}
	if !info.Capabilities.Tools.ListChanged {
		t.Error("Tools.ListChanged should be true")
	}
}

func TestJSONRPCRequest(t *testing.T) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var loaded JSONRPCRequest
	json.Unmarshal(data, &loaded)

	if loaded.Method != "tools/list" {
		t.Errorf("Method = %q, want %q", loaded.Method, "tools/list")
	}
}

func TestJSONRPCResponse(t *testing.T) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  json.RawMessage(`{"tools":[]}`),
	}

	if resp.Error != nil {
		t.Error("Error should be nil for success response")
	}
}

func TestJSONRPCResponse_WithError(t *testing.T) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Error: &JSONRPCError{
			Code:    ErrCodeInternal,
			Message: "internal error",
		},
	}

	if resp.Error == nil {
		t.Fatal("Error should not be nil")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Errorf("Error code = %d, want %d", resp.Error.Code, ErrCodeInternal)
	}
}

func TestToolCallParams(t *testing.T) {
	params := ToolCallParams{
		Name:      "search",
		Arguments: map[string]any{"query": "test"},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var loaded ToolCallParams
	json.Unmarshal(data, &loaded)

	if loaded.Name != "search" {
		t.Errorf("Name = %q, want %q", loaded.Name, "search")
	}
}

func TestToolCallResult(t *testing.T) {
	result := ToolCallResult{
		Content: []ContentBlock{
			{Type: "text", Text: "Found 5 results"},
		},
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content count = %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "Found 5 results" {
		t.Errorf("Content text = %q", result.Content[0].Text)
	}
}

func TestToolCallResult_Error(t *testing.T) {
	result := ToolCallResult{
		Content: []ContentBlock{
			{Type: "text", Text: "Tool failed"},
		},
		IsError: true,
	}

	if !result.IsError {
		t.Error("IsError should be true")
	}
}

func TestContentBlock_Binary(t *testing.T) {
	block := ContentBlock{
		Type:     "image",
		MimeType: "image/png",
		Data:     "base64encodeddata==",
	}

	if block.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want %q", block.MimeType, "image/png")
	}
}

func TestInitializeParams(t *testing.T) {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo: ClientInfo{
			Name:    "vega",
			Version: "1.0.0",
		},
		Capabilities: ClientCapabilities{
			Roots: &RootsCapability{ListChanged: true},
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var loaded InitializeParams
	json.Unmarshal(data, &loaded)

	if loaded.ClientInfo.Name != "vega" {
		t.Errorf("ClientInfo.Name = %q, want %q", loaded.ClientInfo.Name, "vega")
	}
}
