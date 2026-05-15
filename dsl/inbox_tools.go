package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// InboxItem represents a message posted to Iris's inbox by another agent.
type InboxItem struct {
	ID            int64      `json:"id"`
	FromAgent     string     `json:"from_agent"`
	Subject       string     `json:"subject"`
	Body          string     `json:"body,omitempty"`
	Priority      string     `json:"priority"`
	Status        string     `json:"status"`
	Resolution    string     `json:"resolution,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	TriageCount   int        `json:"triage_count"`
	LastTriagedAt *time.Time `json:"last_triaged_at,omitempty"`
}

// InboxBackend is the interface that the store implements for inbox operations.
// Defined here so dsl/ does not import serve/.
type InboxBackend interface {
	InsertInboxItem(fromAgent, subject, body, priority string) (int64, error)
	InsertResolvedInboxItem(fromAgent, subject, body, resolution string) (int64, error)
	ListInboxItems(status string, limit int) ([]InboxItem, error)
	ResolveInboxItem(id int64, resolution string) error
	DeleteInboxItem(id int64) error
	TriageInboxItems(ids []int64, threshold int) ([]int64, error)
}

// DispatchTriageThreshold is the auto-age cutoff used by list_inbox.
// After this many reads without an orchestrator-driven resolution, the
// item gets a synthetic "auto-aged" resolution and disappears from the
// pending queue. Tuned conservatively (3) so a temporary stall during
// the orchestrator's reasoning doesn't drop a real action item.
const DispatchTriageThreshold = 3

// RegisterInboxTools registers the inbox tools on the interpreter.
//
// ask_orchestrator is the canonical name; ask_iris is registered as a
// backward-compat alias for any yaml-defined or composed agent that
// references the old name. Both call the same backing function.
//
// list_inbox and resolve_inbox are added to the orchestrator's tool
// list by the caller (Iris/Apex).
func RegisterInboxTools(interp *Interpreter, backend InboxBackend) {
	t := interp.Tools()

	postFn := tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
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
		return fmt.Sprintf("Message posted to the orchestrator's inbox (id=%d, priority=%s). It will be picked up the next time the orchestrator runs (heartbeat or user message). Do not promise a timeframe.", id, priority), nil
	})

	postParams := map[string]tools.ParamDef{
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
	}

	t.Register("ask_orchestrator", tools.ToolDef{
		Description: "Post a question or request to the orchestrator's inbox. The orchestrator triages the inbox when it next runs (heartbeat or user message) — there is no background processing. Use this instead of asking the user directly.",
		Fn:          postFn,
		Params:      postParams,
	})

	// Backward-compat alias. Same function, same params, same semantics.
	// New agents should use ask_orchestrator; this exists so any yaml or
	// composed agent that still lists "ask_iris" keeps working.
	t.Register("ask_iris", tools.ToolDef{
		Description: "Alias for ask_orchestrator. Post a question or request to the orchestrator's inbox.",
		Fn:          postFn,
		Params:      postParams,
	})

	t.Register("list_inbox", tools.ToolDef{
		Description: "List inbox items. Use this to check for pending questions from agents. The inbox is your decision queue: every item you read MUST exit your turn either resolved (via resolve_inbox) or explicitly deferred. After DispatchTriageThreshold reads without a resolution, items auto-age out so they stop costing tokens.",
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

			// Triage backstop: bump triage_count + auto-age any item
			// that hits the threshold. Only applies when the caller is
			// reading the pending queue — listing resolved or all is
			// passive inspection.
			var agedNote string
			if status == "pending" && len(items) > 0 {
				ids := make([]int64, 0, len(items))
				for _, it := range items {
					ids = append(ids, it.ID)
				}
				aged, terr := backend.TriageInboxItems(ids, DispatchTriageThreshold)
				if terr == nil && len(aged) > 0 {
					// Filter aged items out of the returned list — they
					// flipped to resolved on this call.
					agedSet := make(map[int64]struct{}, len(aged))
					for _, id := range aged {
						agedSet[id] = struct{}{}
					}
					kept := items[:0]
					for _, it := range items {
						if _, ok := agedSet[it.ID]; !ok {
							kept = append(kept, it)
						}
					}
					items = kept
					agedNote = fmt.Sprintf("\n\nNote: %d item(s) auto-aged out (id(s): %v) — they were read %d times without resolution and have been resolved with a synthetic note.", len(aged), aged, DispatchTriageThreshold)
				}
			}

			if len(items) == 0 {
				return "Inbox is empty. No pending items." + agedNote, nil
			}
			out, _ := json.MarshalIndent(items, "", "  ")
			return string(out) + agedNote, nil
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
			var id int64
			switch v := params["id"].(type) {
			case float64:
				id = int64(v)
			case int64:
				id = v
			case int:
				id = int64(v)
			default:
				return "", fmt.Errorf("id is required (positive integer)")
			}
			if id <= 0 {
				return "", fmt.Errorf("id is required (positive integer)")
			}
			resolution, _ := params["resolution"].(string)
			if resolution == "" {
				return "", fmt.Errorf("resolution is required")
			}

			if err := backend.ResolveInboxItem(id, resolution); err != nil {
				return "", fmt.Errorf("resolve inbox item: %w", err)
			}
			return fmt.Sprintf("Inbox item %d resolved.", id), nil
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
