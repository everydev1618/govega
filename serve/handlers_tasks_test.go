package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// taskTestServer returns a *Server wired to a fresh in-memory store —
// enough for handler-level tests without spinning up the full Server.
func taskTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{store: newTestStore(t)}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestHandleCreateTask checks the happy path: POST creates and returns a
// task with id + server-assigned timestamps.
func TestHandleCreateTask(t *testing.T) {
	s := taskTestServer(t)
	body := mustJSON(t, CreateTaskRequest{Title: "Investigate the kanban", Priority: TaskPriorityHigh})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateTask(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got Task
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Title != "Investigate the kanban" || got.Priority != TaskPriorityHigh {
		t.Errorf("unexpected response: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at not set")
	}
}

// TestHandleCreateTask_RejectsEmptyTitle blocks the obvious bad input at the
// boundary — store would reject too, but 400 vs 500 is the right contract.
func TestHandleCreateTask_RejectsEmptyTitle(t *testing.T) {
	s := taskTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"title":"   "}`))
	w := httptest.NewRecorder()
	s.handleCreateTask(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

// TestHandleListTasks_FilterByStatus exercises status= query param.
func TestHandleListTasks_FilterByStatus(t *testing.T) {
	s := taskTestServer(t)
	_ = s.store.InsertTask(Task{ID: "t1", Title: "T1", Status: TaskStatusTodo})
	_ = s.store.InsertTask(Task{ID: "t2", Title: "T2", Status: TaskStatusDoing})
	_ = s.store.InsertTask(Task{ID: "t3", Title: "T3", Status: TaskStatusDone})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=todo,doing", nil)
	w := httptest.NewRecorder()
	s.handleListTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got []Task
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2: %+v", len(got), got)
	}
}

// TestHandleGetTask_IncludesCommentsAndProcesses checks the detail response
// hydrates the activity feed and linked-process list.
func TestHandleGetTask_IncludesCommentsAndProcesses(t *testing.T) {
	s := taskTestServer(t)
	_ = s.store.InsertTask(Task{ID: "tx", Title: "detail"})
	_, _ = s.store.AddTaskComment("tx", "user", "looks good")
	_ = s.store.LinkTaskProcess("tx", "p-1")
	_ = s.store.LinkTaskProcess("tx", "p-2")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/tx", nil)
	req.SetPathValue("id", "tx")
	w := httptest.NewRecorder()
	s.handleGetTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got TaskDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "tx" {
		t.Errorf("id = %q", got.ID)
	}
	if len(got.Comments) != 1 || got.Comments[0].Content != "looks good" {
		t.Errorf("comments = %+v", got.Comments)
	}
	if len(got.Processes) != 2 {
		t.Errorf("processes = %+v", got.Processes)
	}
}

// TestHandleGetTask_NotFound returns 404 with a clean error body.
func TestHandleGetTask_NotFound(t *testing.T) {
	s := taskTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/nope", nil)
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	s.handleGetTask(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestHandleUpdateTask_StatusMove exercises a kanban column drag.
func TestHandleUpdateTask_StatusMove(t *testing.T) {
	s := taskTestServer(t)
	_ = s.store.InsertTask(Task{ID: "u1", Title: "drag me"})

	body := []byte(`{"status":"doing"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/u1", bytes.NewReader(body))
	req.SetPathValue("id", "u1")
	w := httptest.NewRecorder()
	s.handleUpdateTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	saved, _ := s.store.GetTask("u1")
	if saved.Status != TaskStatusDoing {
		t.Errorf("status not applied: %q", saved.Status)
	}
}

// TestHandleUpdateTask_BadStatus returns 400 instead of letting the bad
// value reach the column-rendering layer.
func TestHandleUpdateTask_BadStatus(t *testing.T) {
	s := taskTestServer(t)
	_ = s.store.InsertTask(Task{ID: "u2", Title: "x"})
	body := []byte(`{"status":"in-progress"}`) // not in the enum
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/u2", bytes.NewReader(body))
	req.SetPathValue("id", "u2")
	w := httptest.NewRecorder()
	s.handleUpdateTask(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

// TestHandleAddTaskComment posts a comment and verifies it lands on the task.
func TestHandleAddTaskComment(t *testing.T) {
	s := taskTestServer(t)
	_ = s.store.InsertTask(Task{ID: "c1", Title: "talk to me"})

	body := []byte(`{"content":"first comment","author":"river"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/c1/comments", bytes.NewReader(body))
	req.SetPathValue("id", "c1")
	w := httptest.NewRecorder()
	s.handleAddTaskComment(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	cs, _ := s.store.ListTaskComments("c1")
	if len(cs) != 1 || cs[0].Author != "river" {
		t.Errorf("comments = %+v", cs)
	}
}

// TestHandleLinkTaskProcess attaches a process to a task.
func TestHandleLinkTaskProcess(t *testing.T) {
	s := taskTestServer(t)
	_ = s.store.InsertTask(Task{ID: "lk", Title: "linked"})

	body := []byte(`{"process_id":"p-77"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/lk/processes", bytes.NewReader(body))
	req.SetPathValue("id", "lk")
	w := httptest.NewRecorder()
	s.handleLinkTaskProcess(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	pids, _ := s.store.ListTaskProcesses("lk")
	if len(pids) != 1 || pids[0] != "p-77" {
		t.Errorf("processes = %+v", pids)
	}
}
