package dsl

import (
	"context"
	"time"
)

// Step lifecycle statuses reported to the step observer.
const (
	StepStatusRunning   = "running"
	StepStatusCompleted = "completed"
	StepStatusFailed    = "failed"
)

// StepEvent describes one lifecycle transition of a workflow step. The
// server persists these as run checkpoints (govega#114 Phase 5: workflow
// durability) so an operator can see where an interrupted run died.
type StepEvent struct {
	// Index is the step's position in the workflow's top-level steps.
	Index int `json:"index"`
	// Name labels the step: the agent name for agent steps, otherwise
	// the step kind (set, for, return, ...).
	Name string `json:"name"`
	// Status is one of the StepStatus* constants.
	Status string `json:"status"`
	// Error carries the failure message for StepStatusFailed.
	Error string `json:"error,omitempty"`
	// At is when the transition happened.
	At time.Time `json:"at"`
}

// StepObserver receives step lifecycle events for a workflow run. runID is
// the value attached via ContextWithWorkflowRunID (empty when the caller
// didn't attach one). Called synchronously from the workflow goroutine —
// keep it fast.
type StepObserver func(runID, workflow string, ev StepEvent)

// SetStepObserver installs the step lifecycle observer.
func (i *Interpreter) SetStepObserver(fn StepObserver) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.stepObserver = fn
}

// stepObserverFn returns the installed observer, or nil.
func (i *Interpreter) stepObserverFn() StepObserver {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.stepObserver
}

// workflowRunIDKey is the context key carrying the persisted run ID.
type workflowRunIDKey struct{}

// ContextWithWorkflowRunID attaches the run ID a server allocated for this
// workflow execution, so step events can be correlated with the persisted
// workflow_runs row.
func ContextWithWorkflowRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, workflowRunIDKey{}, runID)
}

// workflowRunIDFromContext returns the attached run ID, or "".
func workflowRunIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(workflowRunIDKey{}).(string)
	return id
}

// stepLabel names a step for checkpoints: agent steps by agent name,
// structural steps by their kind.
func stepLabel(step *Step) string {
	switch {
	case step.Agent != "":
		return step.Agent
	case step.ForEach != "":
		return "for"
	case step.Set != nil:
		return "set"
	case step.Return != "":
		return "return"
	default:
		return "step"
	}
}

// observeStep emits a step lifecycle event if an observer is installed.
func (i *Interpreter) observeStep(ctx context.Context, workflow string, ev StepEvent) {
	fn := i.stepObserverFn()
	if fn == nil {
		return
	}
	ev.At = time.Now().UTC()
	fn(workflowRunIDFromContext(ctx), workflow, ev)
}
