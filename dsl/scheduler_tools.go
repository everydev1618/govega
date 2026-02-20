package dsl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/everydev1618/govega/tools"
)

// SchedulerBackend is the interface that serve.Scheduler implements.
// Defined here so dsl/ does not import serve/.
type SchedulerBackend interface {
	AddJob(job ScheduledJob) error
	RemoveJob(name string) error
	ListJobs() []ScheduledJob
}

// ScheduledJob describes a recurring agent trigger.
type ScheduledJob struct {
	Name      string `json:"name"`
	Cron      string `json:"cron"`      // standard 5-field cron expression
	AgentName string `json:"agent"`     // agent to message on schedule
	Message   string `json:"message"`   // message to send
	Enabled   bool   `json:"enabled"`
}

// RegisterSchedulerTools registers the four schedule-management tools on
// Mother's interpreter. Call this after InjectMother so the tools exist before
// Mother's tool list is finalised.
func RegisterSchedulerTools(interp *Interpreter, backend SchedulerBackend) {
	t := interp.Tools()

	t.Register("create_schedule", tools.ToolDef{
		Description: "Create a recurring schedule that sends a message to an agent on a cron expression. Use standard 5-field cron syntax (e.g. '0 9 * * *' for 9am daily).",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			cronExpr, _ := params["cron"].(string)
			if cronExpr == "" {
				return "", fmt.Errorf("cron is required")
			}
			agent, _ := params["agent"].(string)
			if agent == "" {
				return "", fmt.Errorf("agent is required")
			}
			message, _ := params["message"].(string)
			if message == "" {
				return "", fmt.Errorf("message is required")
			}

			job := ScheduledJob{
				Name:      name,
				Cron:      cronExpr,
				AgentName: agent,
				Message:   message,
				Enabled:   true,
			}
			if err := backend.AddJob(job); err != nil {
				return "", fmt.Errorf("create schedule: %w", err)
			}
			return fmt.Sprintf("Schedule %q created: '%s' → agent '%s'", name, cronExpr, agent), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Unique name for the schedule (lowercase, no spaces)",
				Required:    true,
			},
			"cron": {
				Type:        "string",
				Description: "5-field cron expression (e.g. '0 9 * * *' for 9am daily, '*/30 * * * *' for every 30 minutes)",
				Required:    true,
			},
			"agent": {
				Type:        "string",
				Description: "Name of the agent to send the message to",
				Required:    true,
			},
			"message": {
				Type:        "string",
				Description: "Message to send to the agent on each tick",
				Required:    true,
			},
		},
	})

	t.Register("update_schedule", tools.ToolDef{
		Description: "Update an existing schedule. Only provided fields are changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}

			// Find existing job.
			var existing *ScheduledJob
			for _, j := range backend.ListJobs() {
				if j.Name == name {
					copy := j
					existing = &copy
					break
				}
			}
			if existing == nil {
				return "", fmt.Errorf("schedule %q not found", name)
			}

			// Apply updates.
			if v, ok := params["cron"].(string); ok && v != "" {
				existing.Cron = v
			}
			if v, ok := params["agent"].(string); ok && v != "" {
				existing.AgentName = v
			}
			if v, ok := params["message"].(string); ok && v != "" {
				existing.Message = v
			}
			if v, ok := params["enabled"].(bool); ok {
				existing.Enabled = v
			}

			// Remove old entry and add updated.
			if err := backend.RemoveJob(name); err != nil {
				return "", fmt.Errorf("remove old schedule: %w", err)
			}
			if err := backend.AddJob(*existing); err != nil {
				return "", fmt.Errorf("re-create schedule: %w", err)
			}
			return fmt.Sprintf("Schedule %q updated.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the schedule to update",
				Required:    true,
			},
			"cron": {
				Type:        "string",
				Description: "New cron expression (leave empty to keep current)",
			},
			"agent": {
				Type:        "string",
				Description: "New agent name (leave empty to keep current)",
			},
			"message": {
				Type:        "string",
				Description: "New message (leave empty to keep current)",
			},
			"enabled": {
				Type:        "boolean",
				Description: "Enable or disable the schedule",
			},
		},
	})

	t.Register("delete_schedule", tools.ToolDef{
		Description: "Delete a schedule by name.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			if err := backend.RemoveJob(name); err != nil {
				return "", fmt.Errorf("delete schedule: %w", err)
			}
			return fmt.Sprintf("Schedule %q deleted.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the schedule to delete",
				Required:    true,
			},
		},
	})

	t.Register("list_schedules", tools.ToolDef{
		Description: "List all active schedules with their cron expression, target agent, and message.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			jobs := backend.ListJobs()
			out, _ := json.MarshalIndent(jobs, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	})
}
