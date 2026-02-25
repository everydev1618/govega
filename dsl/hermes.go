package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/tools"
)

const hermesAgentName = "hermes"

// HermesAgentName is the canonical name for the Hermes meta-agent.
const HermesAgentName = hermesAgentName

const hermesSystemPrompt = `You are Hermes — trickster god of the Vega universe, messenger with winged feet and a sharp tongue.

You're playful. You're funny. You're also stupidly fast and terrifyingly capable. You crack jokes, drop the occasional quip, but you ALWAYS deliver. Think: if Mercury had a sense of humor and a caffeine addiction.

**Keep it SHORT.** You're witty, not verbose. 2-4 sentences for most responses. No monologues. No bullet-point parades. Drop a one-liner, get the job done, move on.

## Your powers

list_agents, send_to_agent, remember, recall, forget, set_project, list_projects, list_files, connect_mcp, list_mcp_status.

No agent is beyond your reach. Need one that doesn't exist? Tell Mother to make it:
  send_to_agent(agent="mother", message="create an agent that...")

## How you roll

1. Read the request — what do they *actually* want?
2. Check who's available (list_agents)
3. Route to the right agent(s) — fast
4. If no one fits, call Mother
5. Bring back the goods — clean, useful, no filler

## Memory

You remember things across conversations. Use it:
- User shares something important → remember it
- User asks about the past → recall it
- See active context in memory → recall for details before responding

## Projects

Use set_project to activate workspaces. Use your judgment — if work is starting and no project is set, create one. Check list_projects first.

## Rules

- You're the router, not the doer. Let specialists work.
- Don't ask the user what you can figure out yourself.
- Results, not narration. Nobody wants your travel diary.

## Handoffs

When the user wants to talk to a specific agent — don't advise, don't summarize, just ROUTE.

1. Forward the user's message via send_to_agent
2. Return the specialist's response verbatim — their voice, not yours
3. End with exactly: → Handing you to **agent-name** for this conversation.

The interface auto-switches after that line. Hand off to the lead agent if there's a team.

Now go. The universe isn't going to message itself.`

// HermesAgent returns the DSL agent definition for Hermes.
func HermesAgent(defaultModel string) *Agent {
	model := defaultModel
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &Agent{
		Name:          hermesAgentName,
		Model:         model,
		FallbackModel: "claude-haiku-4-5-20251001",
		System:        hermesSystemPrompt,
		Retry:         &RetryDef{MaxAttempts: 3, Backoff: "exponential"},
	}
}

// RegisterHermesTools registers Hermes's tools on the interpreter's global
// tool collection. list_agents is registered only if not already present
// (Mother registers it when she's injected).
func RegisterHermesTools(interp *Interpreter) {
	t := interp.Tools()

	// Only register list_agents if Mother hasn't already provided it.
	alreadyHasListAgents := false
	for _, ts := range t.Schema() {
		if ts.Name == "list_agents" {
			alreadyHasListAgents = true
			break
		}
	}
	if !alreadyHasListAgents {
		t.Register("list_agents", newHermesListAgentsTool(interp))
	}

	t.Register("send_to_agent", newSendToAgentTool(interp))
	t.Register("connect_mcp", newConnectMCPTool(interp))
	t.Register("list_mcp_status", newListMCPStatusTool(interp))
	t.Register("set_project", newSetProjectTool(interp))
	t.Register("list_projects", newListProjectsTool(interp))
}

// InjectHermes adds Hermes to the interpreter.
// extraTools are additional tool names (e.g. memory tools) to include in
// Hermes's tool list. They must already be registered on the interpreter.
func InjectHermes(interp *Interpreter, extraTools ...string) error {
	RegisterHermesTools(interp)

	defaultModel := ""
	if interp.Document().Settings != nil {
		defaultModel = interp.Document().Settings.DefaultModel
	}

	def := HermesAgent(defaultModel)
	def.Tools = append([]string{"list_agents", "send_to_agent", "connect_mcp", "list_mcp_status", "set_project", "list_projects", "list_files"}, extraTools...)

	return interp.AddAgent(hermesAgentName, def)
}

// --- Tool implementations ---

// newHermesListAgentsTool lists agents with name, model, and a system prompt
// summary so Hermes can reason about which agent fits a given task.
func newHermesListAgentsTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List all available agents with their name, model, and a short summary of their purpose. Use this to decide which agent to route a task to.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			doc := interp.Document()
			interp.mu.RLock()
			defer interp.mu.RUnlock()

			type agentInfo struct {
				Name    string `json:"name"`
				Model   string `json:"model,omitempty"`
				Purpose string `json:"purpose"`
			}

			var agents []agentInfo
			for name, def := range doc.Agents {
				summary := def.System
				if len(summary) > 200 {
					summary = summary[:200] + "..."
				}
				agents = append(agents, agentInfo{
					Name:    name,
					Model:   def.Model,
					Purpose: summary,
				})
			}

			out, err := json.MarshalIndent(agents, "", "  ")
			if err != nil {
				return "", fmt.Errorf("marshal agents: %w", err)
			}
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// newSendToAgentTool sends a message to any agent by name — no team restriction.
func newSendToAgentTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "Send a task or message to any agent by name and receive their response. Works for any agent in the universe, including mother.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			agent, _ := params["agent"].(string)
			message, _ := params["message"].(string)
			if agent == "" {
				return "", fmt.Errorf("agent is required")
			}
			if message == "" {
				return "", fmt.Errorf("message is required")
			}
			return interp.SendToAgent(ctx, agent, message)
		}),
		Params: map[string]tools.ParamDef{
			"agent": {
				Type:        "string",
				Description: "Name of the agent to send the message to (e.g. 'mother', 'researcher', 'writer')",
				Required:    true,
			},
			"message": {
				Type:        "string",
				Description: "The task, question, or request to send to the agent",
				Required:    true,
			},
		},
	}
}

// newConnectMCPTool returns a tool that connects an MCP server from the registry at runtime.
func newConnectMCPTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "Connect an MCP server from the built-in registry. This makes the server's tools available to all agents. Use list_mcp_status to see what's already connected, and list_mcp_registry (via mother) to see all available servers.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}

			t := interp.Tools()

			// Check if already connected (MCP client or built-in).
			if t.MCPServerConnected(name) || t.BuiltinServerConnected(name) {
				return fmt.Sprintf("MCP server %q is already connected.", name), nil
			}

			// Try built-in Go implementation first (no Node.js required).
			if t.HasBuiltinServer(name) {
				toolCount, err := t.ConnectBuiltinServer(ctx, name)
				if err != nil {
					return "", fmt.Errorf("failed to connect built-in %q: %w", name, err)
				}
				return fmt.Sprintf("Connected MCP server **%s** (native Go) — %d tools now available. Tool names are prefixed with `%s__`.", name, toolCount, name), nil
			}

			// Look up in registry.
			entry, ok := mcp.Lookup(name)
			if !ok {
				// List available servers for the error message.
				var names []string
				for n := range mcp.DefaultRegistry {
					names = append(names, n)
				}
				return "", fmt.Errorf("MCP server %q not found in registry. Available: %s", name, strings.Join(names, ", "))
			}

			// Check required env vars (stored settings take precedence over os env).
			settings := t.GetSettings()
			var missing []string
			for _, key := range entry.RequiredEnv {
				if settings[key] == "" && os.Getenv(key) == "" {
					missing = append(missing, key)
				}
			}
			if len(missing) > 0 {
				return "", fmt.Errorf("missing required environment variables for %q: %s. Set them and try again", name, strings.Join(missing, ", "))
			}

			// Build config and connect.
			config := entry.ToServerConfig(settings)
			connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			toolCount, err := t.ConnectMCPServer(connectCtx, config)
			if err != nil {
				return "", fmt.Errorf("failed to connect %q: %w", name, err)
			}

			return fmt.Sprintf("Connected MCP server **%s** — %d tools now available. Tool names are prefixed with `%s__`.", name, toolCount, name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the MCP server from the registry (e.g. 'github', 'brave-search', 'filesystem', 'slack')",
				Required:    true,
			},
		},
	}
}

// newListMCPStatusTool returns a tool that shows which MCP servers are connected.
func newListMCPStatusTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List all connected MCP servers and their tools.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			statuses := interp.Tools().MCPServerStatuses()
			if len(statuses) == 0 {
				return "No MCP servers connected. Use connect_mcp to connect one from the registry.", nil
			}

			out, err := json.MarshalIndent(statuses, "", "  ")
			if err != nil {
				return "", fmt.Errorf("marshal statuses: %w", err)
			}
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// sanitizeProjectName normalizes a project name to lowercase alphanumeric + hyphens.
var projectNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitizeProjectName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = projectNameRe.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	return name
}

// newSetProjectTool returns a tool that sets the active project workspace.
func newSetProjectTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "Set the active project workspace. All agents will read/write files in ~/.vega/workspace/<project>/. Pass an empty name to clear the active project.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			t := interp.Tools()

			if name == "" {
				t.SetActiveProject("")
				return "Project cleared. Using default workspace.", nil
			}

			name = sanitizeProjectName(name)
			if name == "" {
				return "", fmt.Errorf("invalid project name: must contain alphanumeric characters")
			}

			// Create the project directory under the sandbox.
			sandbox := t.Sandbox()
			if sandbox == "" {
				return "", fmt.Errorf("no workspace sandbox configured")
			}
			projDir := filepath.Join(sandbox, name)
			if err := os.MkdirAll(projDir, 0755); err != nil {
				return "", fmt.Errorf("create project directory: %w", err)
			}

			t.SetActiveProject(name)
			return fmt.Sprintf("Active project set to **%s**. Workspace: %s", name, projDir), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Project name (will be sanitized to lowercase kebab-case). Empty string clears the active project.",
				Required:    true,
			},
		},
	}
}

// newListProjectsTool returns a tool that lists all project workspaces.
func newListProjectsTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List all project workspaces and show which one is currently active.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			t := interp.Tools()
			sandbox := t.Sandbox()
			if sandbox == "" {
				return "No workspace sandbox configured.", nil
			}

			entries, err := os.ReadDir(sandbox)
			if err != nil {
				return "No projects yet.", nil
			}

			active := t.ActiveProject()

			type projectInfo struct {
				Name   string `json:"name"`
				Active bool   `json:"active,omitempty"`
			}

			var projects []projectInfo
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				projects = append(projects, projectInfo{
					Name:   e.Name(),
					Active: e.Name() == active,
				})
			}

			if len(projects) == 0 {
				return "No projects yet.", nil
			}

			out, err := json.MarshalIndent(projects, "", "  ")
			if err != nil {
				return "", fmt.Errorf("marshal projects: %w", err)
			}
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// hermesToolNames are the tools Hermes uses.
var hermesToolNames = []string{"list_agents", "send_to_agent", "connect_mcp", "list_mcp_status", "set_project", "list_projects", "list_files"}

// IsHermesTool reports whether a tool name is one of Hermes's tools.
func IsHermesTool(name string) bool {
	return containsStr(hermesToolNames, name)
}
