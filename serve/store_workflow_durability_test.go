package serve

import (
	"testing"
	"time"
)

// TestWorkflowRunStepCheckpoints verifies per-step progress persists on the
// run row and comes back from ListWorkflowRuns.
func TestWorkflowRunStepCheckpoints(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		if err := store.InsertWorkflowRun(WorkflowRun{
			RunID: "run-1", Workflow: "deploy", Inputs: "{}",
			Status: "running", StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("InsertWorkflowRun: %v", err)
		}

		steps := `[{"index":0,"name":"build","status":"completed"},{"index":1,"name":"ship","status":"running"}]`
		if err := store.UpdateWorkflowRunSteps("run-1", steps); err != nil {
			t.Fatalf("UpdateWorkflowRunSteps: %v", err)
		}

		runs, err := store.ListWorkflowRuns(10)
		if err != nil {
			t.Fatalf("ListWorkflowRuns: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("got %d runs, want 1", len(runs))
		}
		if runs[0].Steps != steps {
			t.Errorf("Steps = %q, want the checkpoint JSON", runs[0].Steps)
		}
	})
}

// TestReconcileOrphanedWorkflowRuns verifies rows stuck at status='running'
// (their process died with the server) are marked interrupted at boot, and
// terminal rows are untouched.
func TestReconcileOrphanedWorkflowRuns(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		now := time.Now().UTC()
		mustInsert := func(run WorkflowRun) {
			t.Helper()
			if err := store.InsertWorkflowRun(run); err != nil {
				t.Fatalf("InsertWorkflowRun: %v", err)
			}
		}
		mustInsert(WorkflowRun{RunID: "orphan-1", Workflow: "a", Status: "running", StartedAt: now})
		mustInsert(WorkflowRun{RunID: "orphan-2", Workflow: "b", Status: "running", StartedAt: now})
		mustInsert(WorkflowRun{RunID: "done-1", Workflow: "c", Status: "completed", StartedAt: now})

		n, err := store.ReconcileOrphanedWorkflowRuns()
		if err != nil {
			t.Fatalf("ReconcileOrphanedWorkflowRuns: %v", err)
		}
		if n != 2 {
			t.Errorf("reconciled %d runs, want 2", n)
		}

		runs, err := store.ListWorkflowRuns(10)
		if err != nil {
			t.Fatalf("ListWorkflowRuns: %v", err)
		}
		statuses := map[string]string{}
		for _, r := range runs {
			statuses[r.RunID] = r.Status
		}
		if statuses["orphan-1"] != "interrupted" || statuses["orphan-2"] != "interrupted" {
			t.Errorf("orphans not marked interrupted: %v", statuses)
		}
		if statuses["done-1"] != "completed" {
			t.Errorf("terminal run was touched: %v", statuses)
		}
	})
}
