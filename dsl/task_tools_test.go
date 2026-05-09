package dsl

import (
	"context"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// mockTaskBackend is an in-memory TaskBackend for tool tests. It tracks
// every call so assertions can verify the tools wire to the right backend
// method with the right arguments.
type mockTaskBackend struct {
	tasks    map[string]*Task
	comments []struct{ taskID, author, content string }
	links    []struct{ taskID, processID string }
	calls    []string // human-readable log of method calls
}

func newMockTaskBackend() *mockTaskBackend {
	return &mockTaskBackend{tasks: map[string]*Task{}}
}

func (m *mockTaskBackend) record(s string) { m.calls = append(m.calls, s) }

func (m *mockTaskBackend) InsertTask(t Task) error {
	m.record("InsertTask:" + t.ID + ":" + t.Title + ":assignee=" + t.Assignee + ":priority=" + t.Priority)
	cp := t
	m.tasks[t.ID] = &cp
	return nil
}

func (m *mockTaskBackend) GetTask(id string) (*Task, error) {
	if t, ok := m.tasks[id]; ok {
		return t, nil
	}
	return nil, nil
}

func (m *mockTaskBackend) ListMyTasks(assignee string, status []string, _ int) ([]Task, error) {
	m.record("ListMyTasks:" + assignee + ":" + strings.Join(status, ","))
	out := []Task{}
	for _, t := range m.tasks {
		if t.Assignee != assignee {
			continue
		}
		if len(status) > 0 && !contains(status, t.Status) {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}

func (m *mockTaskBackend) ListUnassignedTasks(_ int) ([]Task, error) {
	m.record("ListUnassignedTasks")
	out := []Task{}
	for _, t := range m.tasks {
		if t.Assignee == "" {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (m *mockTaskBackend) UpdateTaskStatus(id, status string) error {
	m.record("UpdateTaskStatus:" + id + ":" + status)
	if t, ok := m.tasks[id]; ok {
		t.Status = status
	}
	return nil
}

func (m *mockTaskBackend) AssignTask(id, assignee string) error {
	m.record("AssignTask:" + id + ":" + assignee)
	if t, ok := m.tasks[id]; ok {
		t.Assignee = assignee
	}
	return nil
}

func (m *mockTaskBackend) ClaimTask(id, assignee string) error {
	m.record("ClaimTask:" + id + ":" + assignee)
	if t, ok := m.tasks[id]; ok {
		t.Assignee = assignee
		t.Status = "doing"
	}
	return nil
}

func (m *mockTaskBackend) AddTaskComment(taskID, author, content string) (int64, error) {
	m.record("AddTaskComment:" + taskID + ":" + author + ":" + content)
	m.comments = append(m.comments, struct{ taskID, author, content string }{taskID, author, content})
	return int64(len(m.comments)), nil
}

func (m *mockTaskBackend) LinkTaskProcess(taskID, processID string) error {
	m.record("LinkTaskProcess:" + taskID + ":" + processID)
	m.links = append(m.links, struct{ taskID, processID string }{taskID, processID})
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// callTool builds a minimal interpreter, registers the task tools, attaches
// `agentName` as the calling process, and executes a single tool call.
func callTool(t *testing.T, backend *mockTaskBackend, agentName, toolName string, params map[string]any) string {
	t.Helper()
	interp := &Interpreter{
		doc:               &Document{Agents: map[string]*Agent{}},
		agents:            map[string]*vega.Process{},
		tools:             tools.NewTools(),
		delegationConfigs: map[string]*DelegationDef{},
	}
	RegisterTaskTools(interp, backend)

	proc := &vega.Process{ID: "p1", Agent: &vega.Agent{Name: agentName}}
	ctx := vega.ContextWithProcess(context.Background(), proc)

	result, err := interp.Tools().Execute(ctx, toolName, params)
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", toolName, err)
	}
	return result
}

// TestCreateTask_FilesUnassignedByDefault — most common Iris flow: user
// describes work, Iris files it, doesn't yet know who should do it.
func TestCreateTask_FilesUnassignedByDefault(t *testing.T) {
	b := newMockTaskBackend()
	out := callTool(t, b, "iris", "create_task", map[string]any{
		"title":       "Audit kanban offering",
		"description": "Compare what we have vs apps need",
		"priority":    "high",
	})
	if !strings.Contains(out, "routing queue") {
		t.Errorf("response should mention routing queue: %q", out)
	}
	if len(b.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(b.tasks))
	}
	for _, task := range b.tasks {
		if task.Assignee != "" {
			t.Errorf("expected unassigned, got %q", task.Assignee)
		}
		if task.Status != "todo" {
			t.Errorf("expected status=todo, got %q", task.Status)
		}
		if task.CreatedBy != "iris" {
			t.Errorf("expected created_by=iris, got %q", task.CreatedBy)
		}
	}
}

// TestCreateTask_RejectsBadPriority blocks a typo at the boundary.
func TestCreateTask_RejectsBadPriority(t *testing.T) {
	b := newMockTaskBackend()
	interp := &Interpreter{
		doc:               &Document{Agents: map[string]*Agent{}},
		agents:            map[string]*vega.Process{},
		tools:             tools.NewTools(),
		delegationConfigs: map[string]*DelegationDef{},
	}
	RegisterTaskTools(interp, b)
	proc := &vega.Process{ID: "p1", Agent: &vega.Agent{Name: "iris"}}
	ctx := vega.ContextWithProcess(context.Background(), proc)
	_, err := interp.Tools().Execute(ctx, "create_task", map[string]any{
		"title":    "x",
		"priority": "P0",
	})
	if err == nil {
		t.Fatal("expected error for bad priority, got nil")
	}
}

// TestAssignTask_Routes — the orchestrator path. Sets assignee but leaves
// status alone so the worker decides when to start.
func TestAssignTask_Routes(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["t1"] = &Task{ID: "t1", Title: "x", Status: "todo"}

	out := callTool(t, b, "iris", "assign_task", map[string]any{
		"id":       "t1",
		"assignee": "drew",
	})
	if !strings.Contains(out, "drew") {
		t.Errorf("response should name assignee: %q", out)
	}
	if b.tasks["t1"].Assignee != "drew" {
		t.Errorf("assignee not set: %q", b.tasks["t1"].Assignee)
	}
	if b.tasks["t1"].Status != "todo" {
		t.Errorf("assign_task should not move status: %q", b.tasks["t1"].Status)
	}
}

// TestClaimTask_AssignsAndMovesToDoing — the worker path. Atomic claim.
func TestClaimTask_AssignsAndMovesToDoing(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["t1"] = &Task{ID: "t1", Title: "x", Status: "todo"}

	callTool(t, b, "drew", "claim_task", map[string]any{"id": "t1"})

	if b.tasks["t1"].Assignee != "drew" {
		t.Errorf("assignee = %q, want drew", b.tasks["t1"].Assignee)
	}
	if b.tasks["t1"].Status != "doing" {
		t.Errorf("status = %q, want doing", b.tasks["t1"].Status)
	}
}

// TestClaimTask_AutoLinksCallingProcess — the run that claims a task IS
// the run fulfilling it, so humans watching the board can see exactly
// which process is the active worker without anyone calling a separate
// link endpoint.
func TestClaimTask_AutoLinksCallingProcess(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["t1"] = &Task{ID: "t1", Title: "x", Status: "todo"}

	// callTool sets up a process with ID="p1"; verify the claim links it.
	callTool(t, b, "drew", "claim_task", map[string]any{"id": "t1"})

	if len(b.links) != 1 {
		t.Fatalf("expected 1 process link, got %d (calls: %v)", len(b.links), b.calls)
	}
	if b.links[0].taskID != "t1" || b.links[0].processID != "p1" {
		t.Errorf("link = %+v, want {t1, p1}", b.links[0])
	}
}

// TestUpdateTaskStatus_RejectsBadStatus — fails before reaching the store.
func TestUpdateTaskStatus_RejectsBadStatus(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["t1"] = &Task{ID: "t1", Title: "x", Status: "todo"}
	interp := &Interpreter{
		doc:               &Document{Agents: map[string]*Agent{}},
		agents:            map[string]*vega.Process{},
		tools:             tools.NewTools(),
		delegationConfigs: map[string]*DelegationDef{},
	}
	RegisterTaskTools(interp, b)
	proc := &vega.Process{ID: "p1", Agent: &vega.Agent{Name: "drew"}}
	ctx := vega.ContextWithProcess(context.Background(), proc)
	_, err := interp.Tools().Execute(ctx, "update_task_status", map[string]any{
		"id":     "t1",
		"status": "in-progress", // not in the enum
	})
	if err == nil {
		t.Fatal("expected error for bad status")
	}
	// Backend should not have been called.
	for _, c := range b.calls {
		if strings.HasPrefix(c, "UpdateTaskStatus") {
			t.Errorf("backend was called with bad status: %q", c)
		}
	}
}

// TestCommentOnTask_UsesCallerAsAuthor — comments are signed by the
// calling agent, no need for the agent to pass its own name.
func TestCommentOnTask_UsesCallerAsAuthor(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["t1"] = &Task{ID: "t1", Title: "x", Status: "doing"}

	callTool(t, b, "drew", "comment_on_task", map[string]any{
		"id":      "t1",
		"content": "Closed the deal",
	})
	if len(b.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(b.comments))
	}
	if b.comments[0].author != "drew" {
		t.Errorf("author = %q, want drew", b.comments[0].author)
	}
}

// TestListMyTasks_ScopesToCaller — agents only see their own tasks.
func TestListMyTasks_ScopesToCaller(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["a"] = &Task{ID: "a", Title: "drew's", Status: "todo", Assignee: "drew"}
	b.tasks["b"] = &Task{ID: "b", Title: "blake's", Status: "todo", Assignee: "blake"}

	out := callTool(t, b, "drew", "list_my_tasks", map[string]any{})
	if !strings.Contains(out, "drew's") {
		t.Errorf("expected drew's task in output: %q", out)
	}
	if strings.Contains(out, "blake's") {
		t.Errorf("drew should not see blake's task: %q", out)
	}
}

// TestListUnassignedTasks_OrchestratorQueue — the routing queue.
func TestListUnassignedTasks_OrchestratorQueue(t *testing.T) {
	b := newMockTaskBackend()
	b.tasks["a"] = &Task{ID: "a", Title: "queued", Status: "todo", Assignee: ""}
	b.tasks["b"] = &Task{ID: "b", Title: "claimed", Status: "doing", Assignee: "drew"}

	out := callTool(t, b, "iris", "list_unassigned_tasks", map[string]any{})
	if !strings.Contains(out, "queued") {
		t.Errorf("queued task should be listed: %q", out)
	}
	if strings.Contains(out, "claimed") {
		t.Errorf("assigned task should NOT be in routing queue: %q", out)
	}
}
