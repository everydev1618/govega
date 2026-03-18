package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// InboxItem represents a message posted to Hermes's inbox by another agent.
type InboxItem struct {
	ID         int64     `json:"id"`
	FromAgent  string    `json:"from_agent"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body,omitempty"`
	Priority   string    `json:"priority"`
	Status     string    `json:"status"`
	Resolution string    `json:"resolution,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// InboxBackend is the interface that the store implements for inbox operations.
// Defined here so dsl/ does not import serve/.
type InboxBackend interface {
	InsertInboxItem(fromAgent, subject, body, priority string) (int64, error)
	ListInboxItems(status string, limit int) ([]InboxItem, error)
	ResolveInboxItem(id int64, resolution string) error
}

// RegisterInboxTools registers the inbox tools on the interpreter.
// ask_hermes is available to all agents. list_inbox and resolve_inbox are
// added to Hermes's tool list by the caller.
func RegisterInboxTools(interp *Interpreter, backend InboxBackend) {
	t := interp.Tools()

	t.Register("ask_hermes", tools.ToolDef{
		Description: "Post a question or request to Hermes's inbox. Hermes triages the inbox periodically. Use this instead of asking the user directly.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			subject, _ := params["subject"].(string)
			if subject == "" {
				return "", fmt.Errorf("subject is required")
			}
			body, _ := params["body"].(string)
			priority, _ := params["priority"].(string)
			if priority == "" {
				priority = "normal"
			}
			switch priority {
			case "low", "normal", "urgent":
			default:
				return "", fmt.Errorf("priority must be low, normal, or urgent")
			}

			// Determine calling agent name from context.
			fromAgent := "unknown"
			if proc := vega.ProcessFromContext(ctx); proc != nil && proc.Agent != nil {
				fromAgent = proc.Agent.Name
			}

			id, err := backend.InsertInboxItem(fromAgent, subject, body, priority)
			if err != nil {
				return "", fmt.Errorf("post to inbox: %w", err)
			}
			return fmt.Sprintf("Message posted to Hermes's inbox (id=%d, priority=%s). Hermes will review it shortly.", id, priority), nil
		}),
		Params: map[string]tools.ParamDef{
			"subject": {
				Type:        "string",
				Description: "Short summary of the question or request",
				Required:    true,
			},
			"body": {
				Type:        "string",
				Description: "Detailed explanation or context (optional)",
			},
			"priority": {
				Type:        "string",
				Description: "Priority level: low, normal, or urgent (default: normal)",
			},
		},
	})

	t.Register("list_inbox", tools.ToolDef{
		Description: "List inbox items. Use this to check for pending questions from agents.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			status, _ := params["status"].(string)
			if status == "" {
				status = "pending"
			}
			limit := 50
			if v, ok := params["limit"].(float64); ok && v > 0 {
				limit = int(v)
			}

			items, err := backend.ListInboxItems(status, limit)
			if err != nil {
				return "", fmt.Errorf("list inbox: %w", err)
			}
			if len(items) == 0 {
				return "Inbox is empty. No pending items.", nil
			}
			out, _ := json.MarshalIndent(items, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"status": {
				Type:        "string",
				Description: "Filter by status: pending, resolved, or all (default: pending)",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of items to return (default: 50)",
			},
		},
	})

	t.Register("resolve_inbox", tools.ToolDef{
		Description: "Resolve an inbox item by providing a resolution. Use after you've handled an agent's question.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			id, ok := params["id"].(float64)
			if !ok || id <= 0 {
				return "", fmt.Errorf("id is required (positive integer)")
			}
			resolution, _ := params["resolution"].(string)
			if resolution == "" {
				return "", fmt.Errorf("resolution is required")
			}

			if err := backend.ResolveInboxItem(int64(id), resolution); err != nil {
				return "", fmt.Errorf("resolve inbox item: %w", err)
			}
			return fmt.Sprintf("Inbox item %d resolved.", int64(id)), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {
				Type:        "number",
				Description: "ID of the inbox item to resolve",
				Required:    true,
			},
			"resolution": {
				Type:        "string",
				Description: "How the item was resolved (answer given, action taken, escalated, etc.)",
				Required:    true,
			},
		},
	})

}
