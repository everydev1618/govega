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
