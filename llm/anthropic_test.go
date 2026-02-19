package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseToolBlocksToolUse(t *testing.T) {
	content := `Here is my plan.
<tool_use id="toolu_abc" name="create_agent">
{"name":"reviewer","system":"You review code."}
</tool_use>`

	blocks := parseToolBlocks(content)

	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	b0 := blocks[0].(map[string]any)
	if b0["type"] != "text" || b0["text"] != "Here is my plan." {
		t.Errorf("block[0] = %+v", b0)
	}

	b1 := blocks[1].(map[string]any)
	if b1["type"] != "tool_use" || b1["id"] != "toolu_abc" || b1["name"] != "create_agent" {
		t.Errorf("block[1] = %+v", b1)
	}

	input := b1["input"].(map[string]any)
	if input["name"] != "reviewer" {
		t.Errorf("input name = %v, want reviewer", input["name"])
	}
}

func TestParseToolBlocksToolResult(t *testing.T) {
	content := `<tool_result tool_use_id="toolu_abc" name="create_agent">
Agent "reviewer" created successfully.
</tool_result>`

	blocks := parseToolBlocks(content)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	b := blocks[0].(map[string]any)
	if b["type"] != "tool_result" {
		t.Errorf("type = %v, want tool_result", b["type"])
	}
	if b["tool_use_id"] != "toolu_abc" {
		t.Errorf("tool_use_id = %v, want toolu_abc", b["tool_use_id"])
	}
	if b["content"] != `Agent "reviewer" created successfully.` {
		t.Errorf("content = %q", b["content"])
	}
}

func TestParseToolBlocksMultipleToolUse(t *testing.T) {
	content := `I'll create two agents.
<tool_use id="t1" name="create_agent">
{"name":"a"}
</tool_use>
<tool_use id="t2" name="create_agent">
{"name":"b"}
</tool_use>`

	blocks := parseToolBlocks(content)

	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}

	b0 := blocks[0].(map[string]any)
	if b0["type"] != "text" {
		t.Errorf("block[0] type = %v, want text", b0["type"])
	}
	b1 := blocks[1].(map[string]any)
	if b1["id"] != "t1" {
		t.Errorf("block[1] id = %v, want t1", b1["id"])
	}
	b2 := blocks[2].(map[string]any)
	if b2["id"] != "t2" {
		t.Errorf("block[2] id = %v, want t2", b2["id"])
	}
}

func TestParseToolBlocksMultipleToolResults(t *testing.T) {
	content := `<tool_result tool_use_id="t1" name="create_agent">
ok
</tool_result>
<tool_result tool_use_id="t2" name="create_agent">
ok
</tool_result>`

	blocks := parseToolBlocks(content)

	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	b0 := blocks[0].(map[string]any)
	b1 := blocks[1].(map[string]any)
	if b0["tool_use_id"] != "t1" || b1["tool_use_id"] != "t2" {
		t.Errorf("blocks = %+v, %+v", b0, b1)
	}
}

func TestParseToolBlocksPlainText(t *testing.T) {
	content := "Just a regular message, no tools."

	blocks := parseToolBlocks(content)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	b := blocks[0].(map[string]any)
	if b["type"] != "text" || b["text"] != content {
		t.Errorf("block = %+v", b)
	}
}

func TestToolUseInputAlwaysSerialized(t *testing.T) {
	// Tool with no arguments — input must still be present as {} in JSON.
	content := `<tool_use id="t1" name="list_agents">
{}
</tool_use>`

	blocks := parseToolBlocks(content)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	data, err := json.Marshal(blocks[0])
	if err != nil {
		t.Fatal(err)
	}

	// The JSON must contain "input":{} even though the map is empty.
	if !strings.Contains(string(data), `"input":{`) {
		t.Errorf("input field missing from JSON: %s", data)
	}
}

func TestTextBlockHasNoInputField(t *testing.T) {
	content := "Hello world"

	blocks := parseToolBlocks(content)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	data, err := json.Marshal(blocks[0])
	if err != nil {
		t.Fatal(err)
	}

	// Text blocks must NOT have an "input" field.
	if strings.Contains(string(data), `"input"`) {
		t.Errorf("text block should not have input field: %s", data)
	}
}

func TestExtractAttr(t *testing.T) {
	tag := `<tool_use id="toolu_abc123" name="create_agent"`

	if got := extractAttr(tag, "id"); got != "toolu_abc123" {
		t.Errorf("id = %q", got)
	}
	if got := extractAttr(tag, "name"); got != "create_agent" {
		t.Errorf("name = %q", got)
	}
	if got := extractAttr(tag, "missing"); got != "" {
		t.Errorf("missing = %q", got)
	}
}
