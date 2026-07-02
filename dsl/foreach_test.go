package dsl

import (
	"context"
	"testing"
)

// TestForEachExecutesBodySteps verifies a for-each loop parses its body and
// executes the nested steps once per item, binding the loop variable each
// iteration. Regression test for C4 (for-each was a parse + execute no-op).
func TestForEachExecutesBodySteps(t *testing.T) {
	yaml := []byte(`
name: foreach-test
agents:
  noop:
    model: claude-sonnet-4-6
    system: noop
workflows:
  loop:
    steps:
      - set:
          items: ["a", "b", "c"]
      - for: item in items
        steps:
          - set:
              seen: "{{item}}"
      - return: seen
`)

	doc, err := NewParser().Parse(yaml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// The parser must actually populate the loop and its body.
	wf := doc.Workflows["loop"]
	var forStep *Step
	for i := range wf.Steps {
		if wf.Steps[i].ForEach != "" {
			forStep = &wf.Steps[i]
		}
	}
	if forStep == nil {
		t.Fatal("parser did not populate ForEach on the `for` step")
	}
	if len(forStep.Steps) == 0 {
		t.Fatalf("parser did not populate the for-each body steps")
	}

	interp, err := NewInterpreter(doc)
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}

	result, err := interp.RunWorkflow(context.Background(), "loop", map[string]any{})
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	// The body ran for each item in order; `seen` holds the last one.
	if result != "c" {
		t.Errorf("result = %v, want \"c\" (body must execute per item)", result)
	}
}
