package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestClient builds an AnthropicLLM that's safe for buildRequest
// inspection — no API key required because we don't call the network.
func newTestClient(opts ...AnthropicOption) *AnthropicLLM {
	a := &AnthropicLLM{
		model:     DefaultAnthropicModel,
		semaphore: make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func TestBuildRequestAdaptiveThinkingEnabled(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5",
		"claude-sonnet-4-6",
	} {
		t.Run(model, func(t *testing.T) {
			a := newTestClient(WithModel(model))
			req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)
			if req.Thinking == nil {
				t.Fatalf("Thinking nil for %s, want adaptive", model)
			}
			if req.Thinking.Type != "adaptive" {
				t.Errorf("Thinking.Type = %q, want adaptive", req.Thinking.Type)
			}
		})
	}
}

func TestBuildRequestNoThinkingForUnsupportedModels(t *testing.T) {
	for _, model := range []string{
		"claude-haiku-4-5",
		"claude-sonnet-4-5",
		"claude-opus-4-20250514", // original Opus 4.0 — no adaptive
		"some-unknown-model",
	} {
		t.Run(model, func(t *testing.T) {
			a := newTestClient(WithModel(model))
			req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)
			if req.Thinking != nil {
				t.Errorf("Thinking = %+v, want nil for %s", req.Thinking, model)
			}
		})
	}
}

func TestBuildRequestEffortDefaultHigh(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-4-7"))
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	if req.OutputConfig == nil {
		t.Fatalf("OutputConfig nil, want effort populated")
	}
	if req.OutputConfig.Effort != "high" {
		t.Errorf("Effort = %q, want high", req.OutputConfig.Effort)
	}
}

func TestBuildRequestEffortOverride(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-4-7"), WithEffort("xhigh"))
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	if req.OutputConfig == nil || req.OutputConfig.Effort != "xhigh" {
		t.Errorf("Effort = %+v, want xhigh", req.OutputConfig)
	}
}

func TestBuildRequestNoEffortForUnsupportedModels(t *testing.T) {
	for _, model := range []string{
		"claude-haiku-4-5",
		"claude-sonnet-4-5",
		"some-unknown-model",
	} {
		t.Run(model, func(t *testing.T) {
			a := newTestClient(WithModel(model))
			req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)
			if req.OutputConfig != nil {
				t.Errorf("OutputConfig = %+v, want nil for %s", req.OutputConfig, model)
			}
		})
	}
}

func TestBuildRequestNoTemperatureField(t *testing.T) {
	// Temperature is gone — Opus 4.7 returns 400 if it's sent.
	a := newTestClient(WithModel("claude-opus-4-7"))
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"temperature"`) {
		t.Errorf("temperature should not be serialized: %s", body)
	}
}

func TestBuildRequestSerializesAdaptiveAndEffort(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-4-7"), WithEffort("xhigh"))
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	if !strings.Contains(got, `"thinking":{"type":"adaptive"}`) {
		t.Errorf("thinking shape wrong: %s", got)
	}
	if !strings.Contains(got, `"output_config":{"effort":"xhigh"}`) {
		t.Errorf("output_config shape wrong: %s", got)
	}
}

func TestBuildRequestMaxTokensFromCapability(t *testing.T) {
	tests := []struct {
		model        string
		stream       bool
		wantMaxTokens int
	}{
		// Streaming uses the model ceiling
		{"claude-opus-4-7", true, 128000},
		{"claude-opus-4-6", true, 128000},
		{"claude-sonnet-4-6", true, 64000},

		// Non-streaming caps at 16K to dodge SDK HTTP timeout
		{"claude-opus-4-7", false, 16000},
		{"claude-sonnet-4-6", false, 16000},

		// Unknown model: conservative 8K
		{"unknown-model", true, 8192},
		{"unknown-model", false, 8192},
	}

	for _, tt := range tests {
		t.Run(tt.model+"-stream-"+boolStr(tt.stream), func(t *testing.T) {
			a := newTestClient(WithModel(tt.model))
			req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, tt.stream)
			if req.MaxTokens != tt.wantMaxTokens {
				t.Errorf("MaxTokens = %d, want %d", req.MaxTokens, tt.wantMaxTokens)
			}
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestBuildRequestNoBudgetTokens(t *testing.T) {
	// budget_tokens is removed on Opus 4.7 — must not be in any request.
	a := newTestClient(WithModel("claude-opus-4-7"))
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"budget_tokens"`) {
		t.Errorf("budget_tokens must not be serialized: %s", body)
	}
}

// TestBuildRequestCachesTrailingMessage covers govega#3: caching system +
// last tool def isn't enough for long-running agents where the real cost
// is in tool_result content (Galley loads full chapter text). Adding a
// third cache breakpoint on the trailing message implements the rolling
// cache pattern — each call writes cache at the end of conversation, the
// next call reads it.
//
// Contract:
//   * Last message with string content → converted to a single text block
//     carrying cache_control.
//   * Last message with structured blocks (tool_use / tool_result) →
//     cache_control on the LAST block.
//   * Existing system + last-tool breakpoints stay.
func TestBuildRequestCachesTrailingMessage(t *testing.T) {
	t.Run("string content trailing message", func(t *testing.T) {
		a := newTestClient(WithModel("claude-sonnet-4-6"))
		req := a.buildRequest([]Message{
			{Role: RoleSystem, Content: "you are riley."},
			{Role: RoleUser, Content: "what's the weather?"},
		}, nil, false)

		if len(req.Messages) != 1 {
			t.Fatalf("messages len = %d, want 1", len(req.Messages))
		}
		// String content must have been promoted to a single text block
		// with cache_control so the API accepts the marker.
		blocks, ok := req.Messages[0].Content.([]any)
		if !ok {
			t.Fatalf("trailing message Content = %T, want []any with cache_control", req.Messages[0].Content)
		}
		if len(blocks) != 1 {
			t.Fatalf("blocks len = %d, want 1", len(blocks))
		}
		first, _ := blocks[0].(map[string]any)
		if first["type"] != "text" {
			t.Errorf("block type = %v, want text", first["type"])
		}
		if first["text"] != "what's the weather?" {
			t.Errorf("block text = %v, want literal user message", first["text"])
		}
		if _, ok := first["cache_control"].(map[string]any); !ok {
			t.Errorf("trailing block missing cache_control: %+v", first)
		}
	})

	t.Run("structured tool_result trailing block", func(t *testing.T) {
		a := newTestClient(WithModel("claude-sonnet-4-6"))
		toolResult := `<tool_result tool_use_id="x">chapter body</tool_result>`
		req := a.buildRequest([]Message{
			{Role: RoleSystem, Content: "you are the writing guide."},
			{Role: RoleUser, Content: "read chapter 1"},
			{Role: RoleAssistant, Content: `<tool_use id="x" name="read_chapter">{"n":1}</tool_use>`},
			{Role: RoleUser, Content: toolResult},
		}, nil, false)

		if len(req.Messages) == 0 {
			t.Fatal("no messages built")
		}
		last := req.Messages[len(req.Messages)-1]
		blocks, ok := last.Content.([]any)
		if !ok {
			t.Fatalf("last message Content = %T, want []any of blocks", last.Content)
		}
		lastBlock, _ := blocks[len(blocks)-1].(map[string]any)
		if lastBlock["type"] != "tool_result" {
			t.Fatalf("last block type = %v, want tool_result", lastBlock["type"])
		}
		if _, ok := lastBlock["cache_control"].(map[string]any); !ok {
			t.Errorf("tool_result missing cache_control: %+v", lastBlock)
		}
	})

	t.Run("existing system + last-tool breakpoints preserved", func(t *testing.T) {
		a := newTestClient(WithModel("claude-sonnet-4-6"))
		tools := []ToolSchema{
			{Name: "read_file", Description: "read", InputSchema: map[string]any{"type": "object"}},
			{Name: "write_file", Description: "write", InputSchema: map[string]any{"type": "object"}},
		}
		req := a.buildRequest([]Message{
			{Role: RoleSystem, Content: "sys"},
			{Role: RoleUser, Content: "hi"},
		}, tools, false)

		blocks, _ := req.System.([]systemBlock)
		if len(blocks) == 0 || blocks[0].CacheControl == nil {
			t.Error("system prompt cache_control regressed")
		}
		if len(req.Tools) != 2 {
			t.Fatalf("tools len = %d, want 2", len(req.Tools))
		}
		if req.Tools[1].CacheControl == nil {
			t.Error("last tool cache_control regressed")
		}
		if req.Tools[0].CacheControl != nil {
			t.Error("non-last tool unexpectedly has cache_control")
		}
	})

	t.Run("no messages is safe", func(t *testing.T) {
		a := newTestClient(WithModel("claude-sonnet-4-6"))
		// system-only message should still produce a valid request.
		_ = a.buildRequest([]Message{{Role: RoleSystem, Content: "sys"}}, nil, false)
	})
}
