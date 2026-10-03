package vega

import (
	"testing"

	"github.com/everydev1618/govega/llm"
)

// TestEffectiveModel pins which name the logs and the cost ledger use.
//
// Agent.ModelFor names the model we *asked* for. A backend is free to answer
// with a different one — the OpenAI-compatible path falls back to its
// configured model when an override names something the endpoint cannot
// serve. Reporting the request there is how a local LM Studio call came to be
// logged, and billed, as claude-haiku: reportLLMCost reads the vendor off the
// model-name prefix. The model that answered is the honest one.
func TestEffectiveModel(t *testing.T) {
	cases := []struct {
		name      string
		resp      *llm.LLMResponse
		requested string
		want      string
	}{
		{"backend reports what it used", &llm.LLMResponse{Model: "qwen/qwen3.8-27b"},
			"claude-haiku-4-5-20251001", "qwen/qwen3.8-27b"},
		{"backend silent falls back to the request", &llm.LLMResponse{}, "claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"no response at all", nil, "claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"agreement", &llm.LLMResponse{Model: "qwen/qwen3.8-27b"}, "qwen/qwen3.8-27b", "qwen/qwen3.8-27b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveModel(tc.resp, tc.requested); got != tc.want {
				t.Errorf("effectiveModel = %q, want %q", got, tc.want)
			}
		})
	}
}
