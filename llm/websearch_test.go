package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildRequestDeclaresWebToolsWhenEnabled(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-5"), WithWebSearch())
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}},
		[]ToolSchema{{Name: "my_tool", Description: "d", InputSchema: map[string]any{"type": "object"}}}, false)

	var sawSearch, sawFetch, sawCustom bool
	for _, tool := range req.Tools {
		switch tool.Type {
		case "web_search_20260209":
			sawSearch = true
		case "web_fetch_20260209":
			sawFetch = true
		case "":
			if tool.Name == "my_tool" {
				sawCustom = true
			}
		}
	}
	if !sawSearch || !sawFetch || !sawCustom {
		t.Fatalf("tools missing entries: search=%v fetch=%v custom=%v (%+v)",
			sawSearch, sawFetch, sawCustom, req.Tools)
	}

	// Server tools must not carry an input_schema — the API defines their
	// shape, and sending a null schema is a validation error.
	raw, err := json.Marshal(req.Tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if strings.Contains(string(raw), `"input_schema":null`) {
		t.Errorf("a tool serialized a null input_schema:\n%s", raw)
	}
}

func TestBuildRequestOmitsWebToolsByDefault(t *testing.T) {
	a := newTestClient(WithModel("claude-opus-5"))
	req := a.buildRequest([]Message{{Role: RoleUser, Content: "hi"}}, nil, false)
	for _, tool := range req.Tools {
		if tool.Type != "" {
			t.Errorf("server tool %q declared without WithWebSearch", tool.Type)
		}
	}
}

func TestParseResponsePreservesServerToolBlocks(t *testing.T) {
	// A realistic web-search turn: the model searches (server_tool_use), the
	// API attaches results whose content is an ARRAY — un-unmarshalable into
	// the typed contentBlock — then the model writes text.
	body := `{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5",
		"stop_reason":"end_turn",
		"content":[
			{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"latest claude model"}},
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","title":"Claude","url":"https://example.com"}]},
			{"type":"text","text":"Here is what I found."}
		],
		"usage":{"input_tokens":10,"output_tokens":20}
	}`
	var resp anthropicResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	a := newTestClient(WithModel("claude-opus-5"))
	result, err := a.parseResponse(&resp, time.Millisecond)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if result.Content != "Here is what I found." {
		t.Errorf("Content = %q, want the text block", result.Content)
	}
	if len(result.Blocks) != 3 {
		t.Fatalf("got %d blocks, want 3 (two opaque + text)", len(result.Blocks))
	}

	// The replay path: pause_turn continuation re-sends the assistant turn,
	// and the API needs the server-tool blocks back verbatim to resume.
	replay := blocksToAnthropic(result.Blocks)
	raw, err := json.Marshal(replay)
	if err != nil {
		t.Fatalf("marshal replay: %v", err)
	}
	for _, want := range []string{"server_tool_use", "web_search_tool_result", "latest claude model", "Here is what I found."} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("replay missing %q:\n%s", want, raw)
		}
	}
}
