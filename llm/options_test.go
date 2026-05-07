package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionsContextRoundTrip(t *testing.T) {
	temp := 0.7
	opts := Options{
		Model:       "claude-opus-4-7",
		Temperature: &temp,
		MaxTokens:   42000,
		Effort:      "xhigh",
	}
	ctx := ContextWithOptions(context.Background(), opts)

	got := OptionsFromContext(ctx)
	if got.Model != opts.Model {
		t.Errorf("Model = %q, want %q", got.Model, opts.Model)
	}
	if got.Temperature == nil || *got.Temperature != temp {
		t.Errorf("Temperature = %v, want %v", got.Temperature, opts.Temperature)
	}
	if got.MaxTokens != opts.MaxTokens {
		t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, opts.MaxTokens)
	}
	if got.Effort != opts.Effort {
		t.Errorf("Effort = %q, want %q", got.Effort, opts.Effort)
	}
}

func TestOptionsFromContextEmpty(t *testing.T) {
	got := OptionsFromContext(context.Background())
	if got.Model != "" || got.MaxTokens != 0 || got.Effort != "" || got.Temperature != nil {
		t.Errorf("expected zero Options, got %+v", got)
	}
}

func TestBuildRequestOverridesFromContext(t *testing.T) {
	// Client defaults: sonnet-4-6 effort=high. Override via ctx.
	a := newTestClient(WithModel("claude-sonnet-4-6"))
	temp := 0.3
	ctx := ContextWithOptions(context.Background(), Options{
		Model:       "claude-opus-4-7",
		Temperature: &temp, // ignored on Opus 4.7 (no SupportsTemperature)
		MaxTokens:   50000,
		Effort:      "xhigh",
	})

	req := a.buildRequestCtx(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil, true)

	if req.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", req.Model)
	}
	if req.MaxTokens != 50000 {
		t.Errorf("MaxTokens = %d, want 50000", req.MaxTokens)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "xhigh" {
		t.Errorf("Effort = %+v, want xhigh", req.OutputConfig)
	}
	// Opus 4.7 doesn't support temperature — must be dropped silently
	body, _ := json.Marshal(req)
	if strings.Contains(string(body), `"temperature"`) {
		t.Errorf("temperature should not be sent on Opus 4.7: %s", body)
	}
}

func TestBuildRequestTemperatureSentOnSupportedModel(t *testing.T) {
	a := newTestClient(WithModel("claude-sonnet-4-6"))
	temp := 0.5
	ctx := ContextWithOptions(context.Background(), Options{Temperature: &temp})

	req := a.buildRequestCtx(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	body, _ := json.Marshal(req)
	if !strings.Contains(string(body), `"temperature":0.5`) {
		t.Errorf("temperature should be 0.5 for Sonnet 4.6: %s", body)
	}
}

func TestBuildRequestNoOptionsUsesClientDefaults(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-4-7"), WithEffort("medium"))

	// Plain context — no options injected
	req := a.buildRequestCtx(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, false)

	if req.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", req.Model)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "medium" {
		t.Errorf("Effort = %+v, want medium", req.OutputConfig)
	}
}
