package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
	"github.com/google/uuid"
)

// Task is the dsl-side mirror of serve.Task — the same shape, but defined
// here so dsl/ does not import serve/. The serve-side adapter converts
// between the two.
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	Assignee    string     `json:"assignee"`
	Tags        string     `json:"tags"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// TaskBackend is implemented by the store. Defined here so dsl/ stays free
// of a serve/ import.
type TaskBackend interface {
	InsertTask(t Task) error
	GetTask(id string) (*Task, error)
	ListMyTasks(assignee string, status []string, limit int) ([]Task, error)
	ListUnassignedTasks(limit int) ([]Task, error)
	UpdateTaskStatus(id, status string) error
	AssignTask(id, assignee string) error
	ClaimTask(id, assignee string) error // sets assignee=caller, status=doing
	AddTaskComment(taskID, author, content string) (int64, error)
	LinkTaskProcess(taskID, processID string) error
}

// validTaskStatus mirrors serve.validTaskStatus. Kept here so the tool layer
// can reject typos before reaching the store. If the enum changes, both
// sides need to change — small price for keeping serve and dsl decoupled.
func validTaskStatus(s string) bool {
	switch s {
	case "todo", "doing", "blocked", "done", "canceled":
		return true
	}
	return false
}

// validTaskPriority validates the priority field with the same trade-off.
func validTaskPriority(s string) bool {
	switch s {
	case "low", "normal", "high", "urgent":
		return true
	}
	return false
}

// callerAgent returns the agent name making the tool call, or "" if it
// can't be determined. Matches the pattern in inbox_tools.go.
func callerAgent(ctx context.Context) string {
	if proc := vega.ProcessFromContext(ctx); proc != nil && proc.Agent != nil {
		return proc.Agent.Name
	}
	return ""
}

// RegisterTaskTools registers the seven kanban-interaction tools on the
// interpreter. The caller (server.go) decides which to add to which
// agent's tool list — typically all seven for the orchestrator (Iris)
// and a subset for worker agents.
func RegisterTaskTools(interp *Interpreter, backend TaskBackend) {
	t := interp.Tools()

	// list_my_tasks ---------------------------------------------------------
	t.Register("list_my_tasks", tools.ToolDef{
		Description: "List tasks assigned to you. Optionally filter by status (CSV: todo,doing,blocked,done,canceled). Default returns todo+doing+blocked.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			me := callerAgent(ctx)
			if me == "" {
				return "", fmt.Errorf("could not determine calling agent")
			}
			statuses := []string{"todo", "doing", "blocked"}
			if v, ok := params["status"].(string); ok && v != "" {
				statuses = splitCSV(v)
			}
			limit := 50
			if v, ok := params["limit"].(float64); ok && v > 0 {
				limit = int(v)
			}
			tasks, err := backend.ListMyTasks(me, statuses, limit)
			if err != nil {
				return "", fmt.Errorf("list my tasks: %w", err)
			}
			if len(tasks) == 0 {
				return fmt.Sprintf("No tasks assigned to %s with the given status filter.", me), nil
			}
			out, _ := json.MarshalIndent(tasks, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"status": {Type: "string", Description: "Comma-separated statuses to include (default: todo,doing,blocked)"},
			"limit":  {Type: "number", Description: "Max results (default: 50)"},
		},
	})

	// list_unassigned_tasks -------------------------------------------------
	t.Register("list_unassigned_tasks", tools.ToolDef{
		Description: "List tasks with no assignee yet — the orchestrator's routing queue. Use this to triage and assign work to specific agents.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			limit := 50
			if v, ok := params["limit"].(float64); ok && v > 0 {
				limit = int(v)
			}
			tasks, err := backend.ListUnassignedTasks(limit)
			if err != nil {
				return "", fmt.Errorf("list unassigned tasks: %w", err)
			}
			if len(tasks) == 0 {
				return "No unassigned tasks. The routing queue is empty.", nil
			}
			out, _ := json.MarshalIndent(tasks, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"limit": {Type: "number", Description: "Max results (default: 50)"},
		},
	})

	// claim_task ------------------------------------------------------------
	t.Register("claim_task", tools.ToolDef{
		Description: "Claim a task for yourself: sets assignee to you and moves it to 'doing'. Also links your current process to the task so humans watching the board can see exactly which run is fulfilling it. Call this BEFORE starting work — it's the 'I'm on it' signal.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			me := callerAgent(ctx)
			if me == "" {
				return "", fmt.Errorf("could not determine calling agent")
			}
			id, _ := params["id"].(string)
			if id == "" {
				return "", fmt.Errorf("id is required")
			}
			if err := backend.ClaimTask(id, me); err != nil {
				return "", fmt.Errorf("claim task: %w", err)
			}
			// Auto-link the calling process to the task. claim_task is the
			// "I'm starting work on this" verb — the run that calls it IS
			// the run fulfilling the task. Humans watching the kanban see
			// the process appear in the task's Linked Processes list as
			// soon as the worker claims it. Errors here are non-fatal —
			// the claim already succeeded; the link is observability.
			procID := ""
			if proc := vega.ProcessFromContext(ctx); proc != nil {
				procID = proc.ID
			}
			if procID != "" {
				_ = backend.LinkTaskProcess(id, procID)
			}
			return fmt.Sprintf("Task %s claimed by %s and moved to doing. Process %s linked.", id, me, procID), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "string", Description: "Task ID to claim", Required: true},
		},
	})

	// assign_task -----------------------------------------------------------
	t.Register("assign_task", tools.ToolDef{
		Description: "Route a task to a specific agent. Sets the assignee but does NOT move it to 'doing' — the agent decides when to start (claim_task). For orchestrator routing.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			id, _ := params["id"].(string)
			if id == "" {
				return "", fmt.Errorf("id is required")
			}
			assignee, _ := params["assignee"].(string)
			if assignee == "" {
				return "", fmt.Errorf("assignee is required")
			}
			if err := backend.AssignTask(id, assignee); err != nil {
				return "", fmt.Errorf("assign task: %w", err)
			}
			return fmt.Sprintf("Task %s assigned to %s.", id, assignee), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":       {Type: "string", Description: "Task ID", Required: true},
			"assignee": {Type: "string", Description: "Agent name to assign to", Required: true},
		},
	})

	// update_task_status ----------------------------------------------------
	t.Register("update_task_status", tools.ToolDef{
		Description: "Move a task across kanban columns. Statuses: todo, doing, blocked, done, canceled. Use 'blocked' with a comment explaining why.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			id, _ := params["id"].(string)
			if id == "" {
				return "", fmt.Errorf("id is required")
			}
			status, _ := params["status"].(string)
			if !validTaskStatus(status) {
				return "", fmt.Errorf("status must be one of: todo, doing, blocked, done, canceled")
			}
			if err := backend.UpdateTaskStatus(id, status); err != nil {
				return "", fmt.Errorf("update task status: %w", err)
			}
			return fmt.Sprintf("Task %s moved to %s.", id, status), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":     {Type: "string", Description: "Task ID", Required: true},
			"status": {Type: "string", Description: "New status: todo, doing, blocked, done, canceled", Required: true},
		},
	})

	// comment_on_task -------------------------------------------------------
	t.Register("comment_on_task", tools.ToolDef{
		Description: "Add a comment to a task's activity feed. Use this for status updates, findings, blockers, or handoffs — anything a human reviewing the board should see.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			id, _ := params["id"].(string)
			if id == "" {
				return "", fmt.Errorf("id is required")
			}
			content, _ := params["content"].(string)
			if content == "" {
				return "", fmt.Errorf("content is required")
			}
			author := callerAgent(ctx)
			if author == "" {
				author = "agent"
			}
			cid, err := backend.AddTaskComment(id, author, content)
			if err != nil {
				return "", fmt.Errorf("comment on task: %w", err)
			}
			return fmt.Sprintf("Comment %d posted to task %s.", cid, id), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":      {Type: "string", Description: "Task ID", Required: true},
			"content": {Type: "string", Description: "Comment text", Required: true},
		},
	})

	// create_task -----------------------------------------------------------
	t.Register("create_task", tools.ToolDef{
		Description: "File a new task on the kanban. Use when the user describes work to be tracked (versus an immediate one-shot). Leave assignee empty to put it in the routing queue for the orchestrator to assign.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			title, _ := params["title"].(string)
			if title == "" {
				return "", fmt.Errorf("title is required")
			}
			description, _ := params["description"].(string)
			priority, _ := params["priority"].(string)
			if priority == "" {
				priority = "normal"
			}
			if !validTaskPriority(priority) {
				return "", fmt.Errorf("priority must be one of: low, normal, high, urgent")
			}
			assignee, _ := params["assignee"].(string)

			id := newShortID()
			createdBy := callerAgent(ctx)
			if createdBy == "" {
				createdBy = "agent"
			}
			t := Task{
				ID:          id,
				Title:       title,
				Description: description,
				Status:      "todo",
				Priority:    priority,
				Assignee:    assignee,
				CreatedBy:   createdBy,
			}
			if err := backend.InsertTask(t); err != nil {
				return "", fmt.Errorf("create task: %w", err)
			}
			where := "routing queue"
			if assignee != "" {
				where = "assignee=" + assignee
			}
			return fmt.Sprintf("Task %s created (%s). Status=todo, priority=%s.", id, where, priority), nil
		}),
		Params: map[string]tools.ParamDef{
			"title":       {Type: "string", Description: "Short imperative title", Required: true},
			"description": {Type: "string", Description: "Optional details — what done looks like, constraints"},
			"priority":    {Type: "string", Description: "low, normal, high, or urgent (default: normal)"},
			"assignee":    {Type: "string", Description: "Agent name. Leave empty for orchestrator to route."},
		},
	})
}

// splitCSV is a local copy to avoid pulling serve/ helpers across packages.
func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		if r == ' ' && cur == "" {
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// newShortID generates an 8-char id matching the format used by serve
// handlers for tasks created via REST.
func newShortID() string {
	return uuid.New().String()[:8]
}
