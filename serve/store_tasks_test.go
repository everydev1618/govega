package serve

import (
	"testing"
	"time"
)

// TestTask_InsertAndGet checks a task survives a write/read cycle with all
// fields preserved.
func TestTask_InsertAndGet(t *testing.T) {
	store := newTestStore(t)

	due := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	in := Task{
		ID:          "t-1",
		Title:       "Ship the kanban",
		Description: "Replace the dead Process-status board.",
		Status:      TaskStatusTodo,
		Priority:    TaskPriorityHigh,
		Assignee:    "drew",
		Tags:        "ui,kanban",
		CreatedBy:   "user",
		DueAt:       &due,
	}
	if err := store.InsertTask(in); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	got, err := store.GetTask("t-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got == nil {
		t.Fatal("GetTask returned nil")
	}
	if got.Title != in.Title || got.Description != in.Description {
		t.Errorf("title/desc roundtrip failed: %+v", got)
	}
	if got.Status != TaskStatusTodo || got.Priority != TaskPriorityHigh {
		t.Errorf("status/priority roundtrip failed: %+v", got)
	}
	if got.Assignee != "drew" || got.Tags != "ui,kanban" || got.CreatedBy != "user" {
		t.Errorf("assignee/tags/created_by roundtrip failed: %+v", got)
	}
	if got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Errorf("due_at roundtrip failed: got=%v want=%v", got.DueAt, due)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("created_at/updated_at not set: %+v", got)
	}
}

// TestTask_DefaultsApply checks the default status/priority kick in when the
// caller leaves them empty — saves agents from having to know the enum.
func TestTask_DefaultsApply(t *testing.T) {
	store := newTestStore(t)
	if err := store.InsertTask(Task{ID: "t-2", Title: "Naked task"}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}
	got, _ := store.GetTask("t-2")
	if got.Status != TaskStatusTodo {
		t.Errorf("default status = %q, want %q", got.Status, TaskStatusTodo)
	}
	if got.Priority != TaskPriorityNormal {
		t.Errorf("default priority = %q, want %q", got.Priority, TaskPriorityNormal)
	}
}

// TestTask_ListFilter exercises the filter knobs.
func TestTask_ListFilter(t *testing.T) {
	store := newTestStore(t)
	must := func(t Task) {
		if err := store.InsertTask(t); err != nil {
			panic(err)
		}
	}
	must(Task{ID: "a", Title: "A", Status: TaskStatusTodo, Assignee: "drew"})
	must(Task{ID: "b", Title: "B", Status: TaskStatusDoing, Assignee: "drew"})
	must(Task{ID: "c", Title: "C", Status: TaskStatusDone, Assignee: "blake"})
	must(Task{ID: "d", Title: "D", Status: TaskStatusBlocked, Assignee: ""})

	all, err := store.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("len(all) = %d, want 4", len(all))
	}

	open, _ := store.ListTasks(TaskFilter{Status: []string{TaskStatusTodo, TaskStatusDoing, TaskStatusBlocked}})
	if len(open) != 3 {
		t.Errorf("open: len = %d, want 3", len(open))
	}

	drews, _ := store.ListTasks(TaskFilter{Assignee: []string{"drew"}})
	if len(drews) != 2 {
		t.Errorf("drews: len = %d, want 2", len(drews))
	}
}

// TestTask_UpdateTouchesUpdatedAt checks status moves bump updated_at — the
// frontend uses this for ordering and showing recency.
func TestTask_UpdateTouchesUpdatedAt(t *testing.T) {
	store := newTestStore(t)
	if err := store.InsertTask(Task{ID: "t-3", Title: "Move me"}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}
	before, _ := store.GetTask("t-3")
	time.Sleep(1100 * time.Millisecond) // SQLite CURRENT_TIMESTAMP has 1-second resolution

	patch := TaskUpdate{Status: ptr(TaskStatusDoing)}
	if err := store.UpdateTask("t-3", patch); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	after, _ := store.GetTask("t-3")
	if after.Status != TaskStatusDoing {
		t.Errorf("status not applied: %q", after.Status)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("updated_at did not advance: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
}

// TestTask_UpdateRejectsBadStatus refuses unknown statuses at the store layer
// — saves the handler from re-validating.
func TestTask_UpdateRejectsBadStatus(t *testing.T) {
	store := newTestStore(t)
	_ = store.InsertTask(Task{ID: "t-4", Title: "x"})
	err := store.UpdateTask("t-4", TaskUpdate{Status: ptr("nonsense")})
	if err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
}

// TestTask_DeleteCascadesComments verifies FK ON DELETE CASCADE behaves.
func TestTask_DeleteCascadesComments(t *testing.T) {
	store := newTestStore(t)
	_ = store.InsertTask(Task{ID: "t-5", Title: "going away"})
	if _, err := store.AddTaskComment("t-5", "user", "first"); err != nil {
		t.Fatalf("AddTaskComment: %v", err)
	}
	if err := store.DeleteTask("t-5"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	cs, err := store.ListTaskComments("t-5")
	if err != nil {
		t.Fatalf("ListTaskComments: %v", err)
	}
	if len(cs) != 0 {
		t.Errorf("comments survived delete: %d", len(cs))
	}
}

// TestTask_CommentsOrdered ensures comments come back in chronological order.
func TestTask_CommentsOrdered(t *testing.T) {
	store := newTestStore(t)
	_ = store.InsertTask(Task{ID: "t-6", Title: "comment me"})
	for _, body := range []string{"first", "second", "third"} {
		if _, err := store.AddTaskComment("t-6", "user", body); err != nil {
			t.Fatalf("AddTaskComment: %v", err)
		}
		time.Sleep(1100 * time.Millisecond)
	}
	cs, _ := store.ListTaskComments("t-6")
	if len(cs) != 3 {
		t.Fatalf("len = %d, want 3", len(cs))
	}
	if cs[0].Content != "first" || cs[2].Content != "third" {
		t.Errorf("ordering wrong: %+v", cs)
	}
}

// TestTask_LinkProcessIsIdempotent checks linking the same process twice
// doesn't double-count or error — agents may retry.
func TestTask_LinkProcessIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	_ = store.InsertTask(Task{ID: "t-7", Title: "linked"})
	if err := store.LinkTaskProcess("t-7", "p-abc"); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if err := store.LinkTaskProcess("t-7", "p-abc"); err != nil {
		t.Fatalf("second link: %v", err)
	}
	pids, err := store.ListTaskProcesses("t-7")
	if err != nil {
		t.Fatalf("ListTaskProcesses: %v", err)
	}
	if len(pids) != 1 || pids[0] != "p-abc" {
		t.Errorf("links: %+v", pids)
	}
}

func ptr[T any](v T) *T { return &v }
