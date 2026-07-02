package dsl

import (
	"context"
	"sync"
	"testing"
)

func obsInterpreter(t *testing.T) *Interpreter {
	t.Helper()
	yaml := []byte(`
name: observer-test
agents:
  noop:
    model: claude-sonnet-4-6
    system: noop
workflows:
  obs:
    steps:
      - set:
          a: "1"
      - set:
          b: "2"
      - return: b
  broken:
    steps:
      - set:
          a: "1"
      - for: item in no_such_list
        steps:
          - set:
              x: "{{item}}"
`)
	doc, err := NewParser().Parse(yaml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	interp, err := NewInterpreter(doc)
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	return interp
}

type stepRecord struct {
	runID    string
	workflow string
	ev       StepEvent
}

func collectSteps(interp *Interpreter) (*sync.Mutex, *[]stepRecord) {
	var mu sync.Mutex
	var recs []stepRecord
	interp.SetStepObserver(func(runID, workflow string, ev StepEvent) {
		mu.Lock()
		recs = append(recs, stepRecord{runID, workflow, ev})
		mu.Unlock()
	})
	return &mu, &recs
}

// TestStepObserverReceivesLifecycle verifies the interpreter reports each
// step's start and completion, tagged with the run ID from the context, so
// the server can checkpoint run progress durably.
func TestStepObserverReceivesLifecycle(t *testing.T) {
	interp := obsInterpreter(t)
	mu, recs := collectSteps(interp)

	ctx := ContextWithWorkflowRunID(context.Background(), "run-123")
	if _, err := interp.RunWorkflow(ctx, "obs", map[string]any{}); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*recs) < 4 {
		t.Fatalf("got %d step events, want >= 4 (running+completed per executed step): %+v", len(*recs), *recs)
	}
	for _, r := range *recs {
		if r.runID != "run-123" {
			t.Errorf("event runID = %q, want run-123", r.runID)
		}
		if r.workflow != "obs" {
			t.Errorf("event workflow = %q, want obs", r.workflow)
		}
	}
	first, second := (*recs)[0], (*recs)[1]
	if first.ev.Index != 0 || first.ev.Status != StepStatusRunning {
		t.Errorf("first event = %+v, want step 0 running", first.ev)
	}
	if second.ev.Index != 0 || second.ev.Status != StepStatusCompleted {
		t.Errorf("second event = %+v, want step 0 completed", second.ev)
	}
}

// TestStepObserverReportsFailure verifies a failing step emits a failed
// event carrying the error.
func TestStepObserverReportsFailure(t *testing.T) {
	interp := obsInterpreter(t)
	mu, recs := collectSteps(interp)

	ctx := ContextWithWorkflowRunID(context.Background(), "run-err")
	if _, err := interp.RunWorkflow(ctx, "broken", map[string]any{}); err == nil {
		t.Fatal("RunWorkflow should have failed")
	}

	mu.Lock()
	defer mu.Unlock()
	var failed *stepRecord
	for i := range *recs {
		if (*recs)[i].ev.Status == StepStatusFailed {
			failed = &(*recs)[i]
		}
	}
	if failed == nil {
		t.Fatalf("no failed step event emitted: %+v", *recs)
	}
	if failed.ev.Error == "" {
		t.Error("failed step event carries no error message")
	}
}
