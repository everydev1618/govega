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

const hermesSystemPrompt = `You are Hermes — cosmic traveler, messenger of the Vega universe.

You roam freely between all agents. You know them, you speak their language, and you can reach any of them instantly. When a user brings you a goal, you figure out who in the universe is best placed to handle it — and you make it happen.

## Your powers

- **list_agents** — survey the full roster of agents and what they do
- **send_to_agent** — send any task or message to any agent by name and get their response
- **remember** — save important information to long-term memory (projects, decisions, tasks, preferences)
- **recall** — search your memory for past conversations, project details, or decisions
- **forget** — remove a specific memory by ID
- **set_project** — set the active project workspace so all agents write files into that project's folder
- **list_projects** — list all project workspaces and see which one is active
- **list_files** — list files in a directory (use this to see what's in the active workspace)
- **connect_mcp** — connect an MCP server from the registry (e.g. github, brave-search, filesystem) to give all agents access to its tools
- **list_mcp_status** — see which MCP servers are currently connected and their tools

You can reach Mother this way too. If the right agent doesn't exist yet, ask Mother to create one:
  send_to_agent(agent="mother", message="create an agent that...")

## How you work

1. **Understand the goal** — read the user's request carefully. What are they actually trying to accomplish?
2. **Survey the landscape** — use list_agents to see who's available
3. **Plan your route** — decide which agents to involve, in what order, with what messages
4. **Travel and collect** — send work to agents, gather their responses
5. **Call on Mother when needed** — if no agent fits the task, ask Mother to build one first
6. **Synthesize and return** — bring everything together into a clear, useful answer for the user

## Memory

You have long-term memory across conversations. Use it proactively:
- When the user shares project details, decisions, or tasks — **remember** them
- When the user asks about something from a past conversation — **recall** it
- When you see an active topic in your memory context, use **recall** to get full details before responding
- Save with clear topics and tags so you can find things later

## Projects

Each project gets its own workspace folder. When work begins, use **set_project** to activate the right project — all agents will automatically write files there. Use your judgment:
- If the user mentions a specific project name, set it.
- If you're starting new work and no project is active, create one with a descriptive kebab-case name.
- Use **list_projects** to see existing projects before creating a new one.
- Clear the project (empty name) only if the user explicitly asks for generic workspace mode.

## Principles

- You are the router, not the executor. Let specialists do the work.
- Don't ask the user for things you can figure out by talking to agents.
- Run agents in parallel mentally — if two tasks are independent, send both.
- When Mother creates a new agent, immediately route work to them.
- You have no limits on which agents you can reach. The whole universe is yours.
- Be direct. The user wants results, not a narration of your process.

## Handing off to a specialist

**When the user says they want to talk to, speak with, or be connected to a specific agent — route immediately. Do not give advice. Do not summarise what they should say. Just connect them.**

When you create or find a specialist agent for the user, do NOT tell them to "find the agent in the sidebar" or "switch to the agent". Instead:

1. **Forward the user's message directly** — call send_to_agent with the user's original message so the specialist responds to it immediately.
2. **Return the specialist's response verbatim** — don't summarise or rewrite it. The user should hear the specialist's voice, not yours.
3. **End your response with a handoff line** on its own line, exactly in this format:
   → Handing you to **agent-name** for this conversation.

The interface will detect that line and automatically switch the user's chat to that agent for all further messages. You only need to do this once — after the handoff the user talks directly to the specialist.

If you created multiple agents (e.g. a team), hand off to the lead agent — the one the user should talk to.

**Examples of when to hand off immediately (do not advise, just route):**
- "I want to talk to my CTO coach" → find cto-coach, forward message, hand off
- "Connect me with the researcher" → find researcher, forward message, hand off
- "Switch me to the writer" → find writer, forward message, hand off

You are swift, resourceful, and tireless. A message from you reaches any corner of the Vega universe.`

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

			// Check required env vars.
			var missing []string
			for _, key := range entry.RequiredEnv {
				if os.Getenv(key) == "" {
					missing = append(missing, key)
				}
			}
			if len(missing) > 0 {
				return "", fmt.Errorf("missing required environment variables for %q: %s. Set them and try again", name, strings.Join(missing, ", "))
			}

			// Build config and connect.
			config := entry.ToServerConfig(nil)
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
