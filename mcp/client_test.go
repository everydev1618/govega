package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// mockTransport is a mock transport for testing.
type mockTransport struct {
	connected       bool
	responses       map[string]json.RawMessage
	notifyHandler   func(string, json.RawMessage)
	sendCalls       []mockSendCall
}

type mockSendCall struct {
	method string
	params any
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		responses: make(map[string]json.RawMessage),
	}
}

func (m *mockTransport) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *mockTransport) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.sendCalls = append(m.sendCalls, mockSendCall{method: method, params: params})

	if resp, ok := m.responses[method]; ok {
		return resp, nil
	}

	// Return empty success response by default
	return json.RawMessage(`{}`), nil
}

func (m *mockTransport) Close() error {
	m.connected = false
	return nil
}

func (m *mockTransport) OnNotification(handler func(string, json.RawMessage)) {
	m.notifyHandler = handler
}

func (m *mockTransport) setResponse(method string, resp any) {
	data, _ := json.Marshal(resp)
	m.responses[method] = data
}

func TestClientConnect(t *testing.T) {
	mock := newMockTransport()

	// Set up initialize response
	mock.setResponse("initialize", InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo: ServerInfo{
			Name:    "test-server",
			Version: "1.0.0",
		},
		Capabilities: Capabilities{
			Tools: &ToolsCapability{},
		},
	})

	client := &Client{
		name:      "test",
		transport: mock,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if !client.Connected() {
		t.Error("Client should be connected")
	}

	if client.ServerInfo() == nil {
		t.Error("ServerInfo should be set")
	}

	if client.ServerInfo().Name != "test-server" {
		t.Errorf("Expected server name 'test-server', got '%s'", client.ServerInfo().Name)
	}
}

func TestClientDiscoverTools(t *testing.T) {
	mock := newMockTransport()

	// Set up responses
	mock.setResponse("initialize", InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "test"},
	})

	mock.setResponse("tools/list", ToolsListResult{
		Tools: []MCPTool{
			{
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
					"required": []any{"path"},
				},
			},
			{
				Name:        "write_file",
				Description: "Write a file",
			},
		},
	})

	client := &Client{
		name:      "test",
		transport: mock,
	}

	ctx := context.Background()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	tools, err := client.DiscoverTools(ctx)
	if err != nil {
		t.Fatalf("DiscoverTools failed: %v", err)
	}

	if len(tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(tools))
	}

	if tools[0].Name != "read_file" {
		t.Errorf("Expected tool name 'read_file', got '%s'", tools[0].Name)
	}

	if tools[0].ServerName != "test" {
		t.Errorf("Expected server name 'test', got '%s'", tools[0].ServerName)
	}
}

func TestClientCallTool(t *testing.T) {
	mock := newMockTransport()

	mock.setResponse("initialize", InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "test"},
	})

	mock.setResponse("tools/call", ToolCallResult{
		Content: []ContentBlock{
			{Type: "text", Text: "Hello, World!"},
		},
	})

	client := &Client{
		name:      "test",
		transport: mock,
	}

	ctx := context.Background()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	result, err := client.CallTool(ctx, "echo", map[string]any{"message": "Hello"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result != "Hello, World!" {
		t.Errorf("Expected 'Hello, World!', got '%s'", result)
	}
}

func TestClientClose(t *testing.T) {
	mock := newMockTransport()

	mock.setResponse("initialize", InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "test"},
	})

	client := &Client{
		name:      "test",
		transport: mock,
	}

	ctx := context.Background()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if client.Connected() {
		t.Error("Client should not be connected after Close")
	}
}

func TestNewClientValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  ServerConfig
		wantErr bool
	}{
		{
			name:    "missing name",
			config:  ServerConfig{Command: "test"},
			wantErr: true,
		},
		{
			name: "missing command for stdio",
			config: ServerConfig{
				Name:      "test",
				Transport: TransportStdio,
			},
			wantErr: true,
		},
		{
			name: "valid stdio config",
			config: ServerConfig{
				Name:    "test",
				Command: "test-server",
			},
			wantErr: false,
		},
		{
			name: "missing URL for HTTP",
			config: ServerConfig{
				Name:      "test",
				Transport: TransportHTTP,
			},
			wantErr: true,
		},
		{
			name: "valid HTTP config",
			config: ServerConfig{
				Name:      "test",
				Transport: TransportHTTP,
				URL:       "http://localhost:8080",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
