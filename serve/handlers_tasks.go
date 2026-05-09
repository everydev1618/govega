package serve

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// --- Request / response types ---

// CreateTaskRequest is the body of POST /api/v1/tasks.
type CreateTaskRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status,omitempty"`
	Priority    string     `json:"priority,omitempty"`
	Assignee    string     `json:"assignee,omitempty"`
	Tags        string     `json:"tags,omitempty"`
	CreatedBy   string     `json:"created_by,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// TaskDetailResponse is GET /api/v1/tasks/{id}: the task plus its activity
// feed and any processes that have been spawned to fulfill it.
type TaskDetailResponse struct {
	Task
	Comments  []TaskComment `json:"comments"`
	Processes []string      `json:"processes"`
}

// AddTaskCommentRequest is the body of POST /api/v1/tasks/{id}/comments.
type AddTaskCommentRequest struct {
	Author  string `json:"author,omitempty"`
	Content string `json:"content"`
}

// LinkTaskProcessRequest is the body of POST /api/v1/tasks/{id}/processes.
type LinkTaskProcessRequest struct {
	ProcessID string `json:"process_id"`
}

// --- Handlers ---

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := TaskFilter{
		Status:   splitTaskCSV(q.Get("status")),
		Assignee: splitTaskCSV(q.Get("assignee")),
		Tag:      splitTaskCSV(q.Get("tag")),
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		f.Limit = v
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		f.Offset = v
	}

	tasks, err := s.store.ListTasks(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "title is required"})
		return
	}

	t := Task{
		ID:          uuid.New().String()[:8],
		Title:       req.Title,
		Description: req.Description,
		Status:      req.Status,
		Priority:    req.Priority,
		Assignee:    req.Assignee,
		Tags:        req.Tags,
		CreatedBy:   req.CreatedBy,
		DueAt:       req.DueAt,
	}
	if t.CreatedBy == "" {
		t.CreatedBy = "user"
	}

	if err := s.store.InsertTask(t); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Re-read so we return the row with server-set timestamps.
	saved, err := s.store.GetTask(t.ID)
	if err != nil || saved == nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "task created but could not be re-read"})
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.store.GetTask(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if t == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "task not found"})
		return
	}
	comments, err := s.store.ListTaskComments(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	processes, err := s.store.ListTaskProcesses(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, TaskDetailResponse{
		Task:      *t,
		Comments:  comments,
		Processes: processes,
	})
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var u TaskUpdate
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if err := s.store.UpdateTask(id, u); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "task not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	saved, _ := s.store.GetTask(id)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteTask(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "task not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAddTaskComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req AddTaskCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "content is required"})
		return
	}

	t, err := s.store.GetTask(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if t == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "task not found"})
		return
	}

	author := req.Author
	if author == "" {
		author = "user"
	}
	cid, err := s.store.AddTaskComment(id, author, req.Content)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, TaskComment{
		ID:        cid,
		TaskID:    id,
		Author:    author,
		Content:   req.Content,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) handleLinkTaskProcess(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req LinkTaskProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.ProcessID) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "process_id is required"})
		return
	}

	t, err := s.store.GetTask(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if t == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "task not found"})
		return
	}
	if err := s.store.LinkTaskProcess(id, req.ProcessID); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "linked"})
}

// --- Helpers ---

func splitTaskCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
