package serve

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/everydev1618/govega/dsl"
)

// stepCheckpointer folds step lifecycle events into a per-run checkpoint
// list and persists it on the workflow_runs row after every transition, so
// an interrupted run shows exactly which step it died in.
type stepCheckpointer struct {
	mu   sync.Mutex
	runs map[string][]dsl.StepEvent
}

// record folds ev into the run's step list: a transition for an index the
// run has already seen replaces that entry (running → completed/failed);
// new indexes append. Returns the updated list as JSON.
func (c *stepCheckpointer) record(runID string, ev dsl.StepEvent) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runs == nil {
		c.runs = make(map[string][]dsl.StepEvent)
	}
	steps := c.runs[runID]
	replaced := false
	for i := range steps {
		if steps[i].Index == ev.Index {
			steps[i] = ev
			replaced = true
			break
		}
	}
	if !replaced {
		steps = append(steps, ev)
	}
	c.runs[runID] = steps

	b, err := json.Marshal(steps)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// forget drops the run's in-memory state once the run reaches a terminal
// status.
func (c *stepCheckpointer) forget(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.runs, runID)
}

// wireStepObserver hooks workflow step lifecycle events into the store
// (checkpoints on the run row) and the broker (live step events for the UI).
func (s *Server) wireStepObserver() {
	s.interp.SetStepObserver(func(runID, workflow string, ev dsl.StepEvent) {
		// Publish regardless of persistence — ad-hoc runs without a run
		// ID still show live step progress.
		s.broker.Publish(BrokerEvent{
			Type:      "workflow.step",
			Timestamp: time.Now(),
			Data: map[string]string{
				"run_id":   runID,
				"workflow": workflow,
				"step":     ev.Name,
				"index":    strconv.Itoa(ev.Index),
				"status":   ev.Status,
				"error":    ev.Error,
			},
		})

		if runID == "" {
			return // not a persisted run
		}
		stepsJSON := s.stepCheckpoints.record(runID, ev)
		if err := s.store.UpdateWorkflowRunSteps(runID, stepsJSON); err != nil {
			slog.Warn("workflow step checkpoint persist failed", "run_id", runID, "error", err)
		}
	})
}
