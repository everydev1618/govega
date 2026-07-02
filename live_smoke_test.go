package vega

// Live API smoke tests: run against the real Anthropic API to validate the
// pieces fakes cannot — that the API *accepts* what govega now sends.
// Specifically: adaptive thinking on current models, thinking-block replay
// with signatures during tool loops (the structured-blocks refactor), typed
// tool_result blocks, prompt-cache breakpoints on typed blocks, and cost
// accounting from real usage numbers.
//
// Gated: set VEGA_E2E_LIVE=1 and ANTHROPIC_API_KEY. Costs a few cents per
// run on claude-sonnet-4-6. Not part of default CI — run before releases
// and after any model/table or block-format change.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

const liveModel = "claude-sonnet-4-6"

func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("VEGA_E2E_LIVE") != "1" {
		t.Skip("live API smoke disabled; set VEGA_E2E_LIVE=1 to run")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
}

func liveEchoTools() *tools.Tools {
	ts := tools.NewTools()
	ts.Register("echo_marker", tools.ToolDef{
		Description: "Echoes the given text back, wrapped in a marker. Use it whenever asked to echo something.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			text, _ := params["text"].(string)
			return "MARKER<" + text + ">MARKER", nil
		}),
		Params: map[string]tools.ParamDef{
			"text": {Type: "string", Required: true, Description: "text to echo"},
		},
	})
	return ts
}

func liveAgent(system string) Agent {
	return Agent{
		Name:   "live-smoke",
		Model:  liveModel,
		System: StaticPrompt(system),
		Tools:  liveEchoTools(),
	}
}

// TestLiveToolRoundTripWithThinking is the critical post-refactor check:
// a full tool loop on an adaptive-thinking model. The second request of
// the turn replays our typed blocks — thinking (text + signature),
// tool_use, and tool_result. If the API rejects any of that shape, this
// fails with a 400.
func TestLiveToolRoundTripWithThinking(t *testing.T) {
	requireLive(t)

	o := NewOrchestrator(WithLLM(llm.NewAnthropic(llm.WithModel(liveModel))))
	proc, err := o.Spawn(liveAgent("You are a test agent. When asked to echo something, you MUST use the echo_marker tool and then report its exact output."))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Complete("done")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := proc.Send(ctx, "Echo the text live-e2e-42 and tell me exactly what the tool returned.")
	if err != nil {
		t.Fatalf("tool round trip rejected by the API: %v", err)
	}
	if !strings.Contains(resp, "live-e2e-42") {
		t.Errorf("response does not reference the tool output: %q", resp)
	}

	m := proc.Metrics()
	if m.ToolCalls == 0 {
		t.Error("model never called the tool")
	}
	if m.InputTokens == 0 || m.OutputTokens == 0 {
		t.Errorf("usage not captured: %+v", m)
	}
	if m.CostUSD <= 0 {
		t.Errorf("cost not accounted: %+v", m)
	}
	t.Logf("live tool round trip: in=%d out=%d toolCalls=%d cost=$%.5f",
		m.InputTokens, m.OutputTokens, m.ToolCalls, m.CostUSD)
}

// TestLiveStreamingToolRoundTrip covers the streaming path: thinking
// events (including signature deltas) assembled by the block collector and
// replayed mid-turn.
func TestLiveStreamingToolRoundTrip(t *testing.T) {
	requireLive(t)

	o := NewOrchestrator(WithLLM(llm.NewAnthropic(llm.WithModel(liveModel))))
	proc, err := o.Spawn(liveAgent("You are a test agent. When asked to echo something, you MUST use the echo_marker tool and then report its exact output."))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Complete("done")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stream, err := proc.SendStream(ctx, "Echo the text stream-e2e-77 and tell me exactly what the tool returned.")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	var text string
	for chunk := range stream.Chunks() {
		text += chunk
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("streaming tool round trip rejected by the API: %v", err)
	}
	if !strings.Contains(text, "stream-e2e-77") {
		t.Errorf("streamed response does not reference the tool output: %q", text)
	}
	if m := proc.Metrics(); m.ToolCalls == 0 {
		t.Error("model never called the tool on the streaming path")
	}
}

// TestLivePromptCacheAndCost verifies the cache_control breakpoints still
// land where the API accepts them after the block refactor: a second turn
// on the same conversation must read from cache. Requires a system prompt
// above the model's minimum cacheable prefix (~2048 tokens on sonnet-4-6).
func TestLivePromptCacheAndCost(t *testing.T) {
	requireLive(t)

	// ~3000 tokens of stable system prompt so the prefix is cacheable.
	filler := strings.Repeat("The quick brown fox jumps over the lazy dog near the riverbank at dawn. ", 700)
	o := NewOrchestrator(WithLLM(llm.NewAnthropic(llm.WithModel(liveModel))))
	proc, err := o.Spawn(liveAgent("You are a terse test agent. Answer in one short sentence. Background context (ignore it): " + filler))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Complete("done")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if _, err := proc.Send(ctx, "Say the word one."); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if _, err := proc.Send(ctx, "Say the word two."); err != nil {
		t.Fatalf("second turn: %v", err)
	}

	m := proc.Metrics()
	if m.CacheCreationInputTokens == 0 {
		t.Errorf("no cache writes recorded — cache_control breakpoints not accepted? %+v", m)
	}
	if m.CacheReadInputTokens == 0 {
		t.Errorf("no cache reads on the second turn — prefix invalidated between turns? %+v", m)
	}
	if m.CostUSD <= 0 {
		t.Errorf("cost not accounted: %+v", m)
	}
	t.Logf("live cache: created=%d read=%d cost=$%.5f",
		m.CacheCreationInputTokens, m.CacheReadInputTokens, m.CostUSD)
}
