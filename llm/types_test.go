package llm

import (
	"math"
	"testing"
)

func TestRoles(t *testing.T) {
	if RoleUser != "user" {
		t.Errorf("RoleUser = %q, want %q", RoleUser, "user")
	}
	if RoleAssistant != "assistant" {
		t.Errorf("RoleAssistant = %q, want %q", RoleAssistant, "assistant")
	}
	if RoleSystem != "system" {
		t.Errorf("RoleSystem = %q, want %q", RoleSystem, "system")
	}
}

func TestStopReasons(t *testing.T) {
	tests := []struct {
		reason StopReason
		want   string
	}{
		{StopReasonEnd, "end_turn"},
		{StopReasonToolUse, "tool_use"},
		{StopReasonLength, "max_tokens"},
		{StopReasonStop, "stop_sequence"},
		{StopReasonFiltered, "content_filter"},
	}

	for _, tt := range tests {
		if string(tt.reason) != tt.want {
			t.Errorf("StopReason = %q, want %q", tt.reason, tt.want)
		}
	}
}

func TestStreamEventTypes(t *testing.T) {
	tests := []struct {
		et   StreamEventType
		want string
	}{
		{StreamEventMessageStart, "message_start"},
		{StreamEventContentStart, "content_start"},
		{StreamEventContentDelta, "content_delta"},
		{StreamEventContentEnd, "content_end"},
		{StreamEventToolStart, "tool_start"},
		{StreamEventToolDelta, "tool_delta"},
		{StreamEventToolEnd, "tool_end"},
		{StreamEventMessageEnd, "message_end"},
		{StreamEventError, "error"},
	}

	for _, tt := range tests {
		if string(tt.et) != tt.want {
			t.Errorf("StreamEventType = %q, want %q", tt.et, tt.want)
		}
	}
}

func TestCalculateCost_KnownModel(t *testing.T) {
	// claude-sonnet-4-20250514: $3.00/1M input, $15.00/1M output
	cost := CalculateCost("claude-sonnet-4-20250514", 1000, 500, 0, 0)

	expectedInput := 1000.0 / 1_000_000 * 3.00
	expectedOutput := 500.0 / 1_000_000 * 15.00
	expected := expectedInput + expectedOutput

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("CalculateCost = %f, want %f", cost, expected)
	}
}

func TestCalculateCost_UnknownModel(t *testing.T) {
	// Unknown model should use default (sonnet) pricing
	cost := CalculateCost("unknown-model-123", 1000, 500, 0, 0)

	// Should not be zero - uses default pricing
	if cost == 0 {
		t.Error("CalculateCost for unknown model should use default pricing")
	}

	// Should equal sonnet pricing
	costSonnet := CalculateCost("claude-sonnet-4-20250514", 1000, 500, 0, 0)
	if math.Abs(cost-costSonnet) > 0.0001 {
		t.Errorf("Unknown model cost = %f, should match sonnet = %f", cost, costSonnet)
	}
}

func TestCalculateCost_WithCacheTokens(t *testing.T) {
	// Cache writes cost 125% of input, cache reads cost 10% of input
	cost := CalculateCost("claude-sonnet-4-20250514", 0, 0, 1000, 1000)

	// $3.00/1M input
	cacheWriteCost := 1000.0 / 1_000_000 * 3.00 * 1.25
	cacheReadCost := 1000.0 / 1_000_000 * 3.00 * 0.10
	expected := cacheWriteCost + cacheReadCost

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("CalculateCost with cache = %f, want %f", cost, expected)
	}
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	cost := CalculateCost("claude-sonnet-4-20250514", 0, 0, 0, 0)
	if cost != 0 {
		t.Errorf("CalculateCost with zero tokens = %f, want 0", cost)
	}
}

func TestCalculateCost_OpusModel(t *testing.T) {
	// claude-opus-4-20250514: $15.00/1M input, $75.00/1M output
	cost := CalculateCost("claude-opus-4-20250514", 1_000_000, 1_000_000, 0, 0)

	expected := 15.00 + 75.00
	if math.Abs(cost-expected) > 0.01 {
		t.Errorf("Opus cost = %f, want %f", cost, expected)
	}
}

func TestCalculateCost_HaikuModel(t *testing.T) {
	// claude-haiku-3-20240307: $0.25/1M input, $1.25/1M output
	cost := CalculateCost("claude-haiku-3-20240307", 1_000_000, 1_000_000, 0, 0)

	expected := 0.25 + 1.25
	if math.Abs(cost-expected) > 0.01 {
		t.Errorf("Haiku cost = %f, want %f", cost, expected)
	}
}

func TestLLMResponse(t *testing.T) {
	resp := &LLMResponse{
		Content:      "Hello, world!",
		InputTokens:  100,
		OutputTokens: 50,
		CostUSD:      0.001,
		StopReason:   StopReasonEnd,
		ToolCalls: []ToolCall{
			{ID: "call-1", Name: "search", Arguments: map[string]any{"query": "test"}},
		},
	}

	if resp.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world!")
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("ToolCalls length = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("ToolCall name = %q, want %q", resp.ToolCalls[0].Name, "search")
	}
}

func TestToolSchema(t *testing.T) {
	schema := ToolSchema{
		Name:        "read_file",
		Description: "Read a file from disk",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path to read",
				},
			},
			"required": []string{"path"},
		},
	}

	if schema.Name != "read_file" {
		t.Errorf("Schema name = %q, want %q", schema.Name, "read_file")
	}
	props := schema.InputSchema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Error("Schema should have 'path' property")
	}
}

func TestMessage(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "Hello"}
	if msg.Role != RoleUser {
		t.Errorf("Role = %q, want %q", msg.Role, RoleUser)
	}
	if msg.Content != "Hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello")
	}
}

func TestStreamEvent(t *testing.T) {
	ev := StreamEvent{
		Type:         StreamEventContentDelta,
		Delta:        "hello",
		InputTokens:  50,
		OutputTokens: 25,
	}

	if ev.Type != StreamEventContentDelta {
		t.Errorf("Type = %q, want %q", ev.Type, StreamEventContentDelta)
	}
	if ev.Delta != "hello" {
		t.Errorf("Delta = %q, want %q", ev.Delta, "hello")
	}
}
