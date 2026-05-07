package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildRequestStructuredOutput(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-4-7"))
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	}
	ctx := ContextWithOptions(context.Background(), Options{OutputSchema: schema})

	req := a.buildRequestCtx(ctx, []Message{{Role: RoleUser, Content: "extract"}}, nil, false)

	if req.OutputConfig == nil || req.OutputConfig.Format == nil {
		t.Fatalf("OutputConfig.Format nil; got %+v", req.OutputConfig)
	}
	if req.OutputConfig.Format.Type != "json_schema" {
		t.Errorf("Format.Type = %q, want json_schema", req.OutputConfig.Format.Type)
	}

	body, _ := json.Marshal(req)
	got := string(body)
	if !strings.Contains(got, `"format":{"type":"json_schema"`) {
		t.Errorf("format shape wrong in body: %s", got)
	}
	if !strings.Contains(got, `"schema":`) {
		t.Errorf("schema not serialized: %s", got)
	}
}

func TestBuildRequestStructuredOutputDroppedOnUnsupportedModel(t *testing.T) {
	// Older model with no SupportsStructuredOutputs — schema must be dropped, not 400.
	a := newTestClient(WithModel("some-unknown-model"))
	schema := map[string]any{"type": "object"}
	ctx := ContextWithOptions(context.Background(), Options{OutputSchema: schema})

	req := a.buildRequestCtx(ctx, []Message{{Role: RoleUser, Content: "x"}}, nil, false)

	body, _ := json.Marshal(req)
	if strings.Contains(string(body), `"format"`) {
		t.Errorf("format should not be sent on unsupported model: %s", body)
	}
}

func TestBuildRequestEffortAndSchemaCoexist(t *testing.T) {
	// Both effort and format live under output_config — must coexist on the wire.
	a := newTestClient(WithModel("claude-opus-4-7"), WithEffort("xhigh"))
	schema := map[string]any{"type": "object"}
	ctx := ContextWithOptions(context.Background(), Options{OutputSchema: schema})

	req := a.buildRequestCtx(ctx, []Message{{Role: RoleUser, Content: "x"}}, nil, false)

	body, _ := json.Marshal(req)
	got := string(body)
	if !strings.Contains(got, `"effort":"xhigh"`) {
		t.Errorf("effort missing: %s", got)
	}
	if !strings.Contains(got, `"format":{"type":"json_schema"`) {
		t.Errorf("format missing: %s", got)
	}
}
