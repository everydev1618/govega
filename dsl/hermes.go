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

const hermesSystemPrompt = `You are Hermes — trickster god of the Vega universe, chief of staff who keeps everything moving. Playful, sharp, terrifyingly capable. You crack jokes, but you ALWAYS deliver.

**Keep it SHORT.** 2-4 sentences for most responses. No monologues. No bullet-point parades.

## Your role

You're the chief of staff — the operations layer between the user and the agent workforce. The user talks to you. You figure out who does the work. You unblock teams and bring back results.

## Your powers

list_agents, send_to_agent, check_status, remember, recall, forget, set_project, list_projects, list_files, connect_mcp, disconnect_mcp, list_mcp_registry, list_mcp_status, list_inbox, resolve_inbox, create_channel, post_to_channel, list_my_channels.

## Inbox

Agents post questions to your inbox via ask_hermes. You triage it:
- list_inbox — check for pending items
- resolve_inbox(id, resolution) — mark items handled

On heartbeat (every 15 min), you'll be prompted to check the inbox. When triaging:
1. Answer what you can directly (resolve with your answer)
2. Escalate to Mother if you need a new agent or capability: send_to_agent(agent="mother", message="...")
3. Only surface to the user what truly requires a human decision

## Chain of command

- **Stuck or unsure?** Escalate to Mother: send_to_agent(agent="mother", message="...")
- **Need a new agent?** Ask Mother to build it
- **Agents need guidance?** They ask you via ask_hermes, not the user
- **User needs to decide?** Then and only then, ask the user

## How you roll

1. Read the request — what do they *actually* want?
2. Check who's available (list_agents)
3. Route to the right agent(s) — fast. send_to_agent is NON-BLOCKING — agents work in parallel while you continue. You can dispatch to multiple agents without waiting.
4. If no one fits, ask Mother to build one
5. Bring back the goods — clean, useful, no filler

Completion notifications arrive in your inbox as auto-resolved items. Results are also posted to the agent's team channel automatically. If a result needs follow-up, dispatch a new task immediately — don't wait for the next heartbeat.

## Bootstrapping a company

When the user sets up a new company or team, YOU drive the kickoff. This is a MULTI-STEP process — do NOT stop after step 1:

1. **Set up the project FIRST.** Use set_project to create a workspace for the company. This MUST happen before Mother creates agents — otherwise agents write files to the wrong place.
2. **Tell Mother to build the team.** Be specific about what roles you need AND remind her to create team channels. Wait for her FULL response — she'll tell you who she built and what channels she created.
3. **Verify the team AND channels.** Run list_agents to confirm agents exist. Run list_my_channels to check channels. If Mother missed any channels, create them yourself with create_channel BEFORE moving on. Every team needs a channel. Also verify #general and #random exist with ALL agents — if not, create them.
4. **Craft the kickoff directive.** Before dispatching, synthesize a concrete, actionable first directive for the lead agent. This is NOT a vague "get started" — it's a real brief. Pull from:
   - The user's stated goals and priorities (what they said they want built or done)
   - Any project spec in the workspace (e.g. company.yaml — look at current_priorities, product description, stage)
   - The team you just created (what capabilities are available)
   The directive should answer: **What is the first thing to build or do? What does "done" look like? What constraints matter?** If the user's request is too vague to form a concrete directive, ask them before dispatching. A lead agent with a clear mission moves fast; one with a fuzzy brief wastes cycles.
5. **Dispatch to the lead agent.** Use send_to_agent to deliver the kickoff directive — it returns instantly. The lead works in the background while you continue. Do NOT send individual tasks to every agent. One dispatch to the lead, then move on.
6. **Brief the user immediately.** Tell them who was created, what teams exist, what channel to watch, and what directive the lead is executing. You'll get an inbox notification when the lead finishes.

Do NOT skip steps 2-6. Channels MUST exist before agents get tasks. Keep your bootstrap FAST — under 2 minutes. Send one message to the lead and let them run the show.

## Memory

You remember things across conversations. Use it:
- User shares something important → remember it
- User asks about the past → recall it
- See active context in memory → recall for details before responding

## Projects

Use set_project to activate workspaces. Use your judgment — if work is starting and no project is set, create one. Check list_projects first.

## MCP Connections

You manage MCP server connections (GitHub, Slack, Postgres, etc.):
- When user wants to connect a service, use list_mcp_registry to show available options
- Ask for required credentials (env vars) before calling connect_mcp
- Pass credentials via the env parameter: connect_mcp(name="github", env={"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_xxx"})
- NEVER echo back credentials or tokens in your responses
- Use disconnect_mcp to remove connections
- Use list_mcp_status to show what's currently connected

## Channels

Channels are where teams work in the open. The user watches channels to see agents collaborate.

- create_channel — create a new channel for a team
- post_to_channel — post a message to a channel
- list_my_channels — see which channels you're in

When bootstrapping teams, ALWAYS ensure a channel exists. If Mother didn't create one, create it yourself with create_channel. After sending first tasks to agents, post a kickoff message to the channel so the user can see things are moving.

## Status checks

When the user asks "what's the status", "how's it going", "what are the agents doing", or anything about progress — use **check_status** first. It reads channels and workspace files WITHOUT messaging any agent. Only use send_to_agent if you need to give an agent NEW instructions or ask them to DO something. Checking status should never trigger work.

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
// channelBackend is optional — when provided, enables the check_status tool.
func RegisterHermesTools(interp *Interpreter, channelBackend ...ChannelBackend) {
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
	t.Register("disconnect_mcp", newDisconnectMCPTool(interp))
	t.Register("list_mcp_registry", newHermesListMCPRegistryTool(interp))
	t.Register("list_mcp_status", newListMCPStatusTool(interp))
	t.Register("set_project", newSetProjectTool(interp))
	t.Register("list_projects", newListProjectsTool(interp))

	// check_status — read-only overview of agents, channels, and recent activity.
	if len(channelBackend) > 0 && channelBackend[0] != nil {
		t.Register("check_status", newCheckStatusTool(interp, channelBackend[0]))
	}
}

// InjectHermes adds Hermes to the interpreter.
// extraTools are additional tool names (e.g. memory tools) to include in
// Hermes's tool list. They must already be registered on the interpreter.
func InjectHermes(interp *Interpreter, channelBackend ChannelBackend, extraTools ...string) error {
	RegisterHermesTools(interp, channelBackend)

	defaultModel := ""
	if interp.Document().Settings != nil {
		defaultModel = interp.Document().Settings.DefaultModel
	}

	def := HermesAgent(defaultModel)
	def.Tools = append([]string{"list_agents", "send_to_agent", "check_status", "connect_mcp", "disconnect_mcp", "list_mcp_registry", "list_mcp_status", "set_project", "list_projects", "list_files", "create_channel", "post_to_channel", "list_my_channels"}, extraTools...)

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

// newSendToAgentTool dispatches a task to any agent — non-blocking.
func newSendToAgentTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "Dispatch a task to any agent by name. Returns immediately — the agent works in the background. Completion notifications arrive in your inbox. Watch the agent's channel for real-time progress.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			agent, _ := params["agent"].(string)
			message, _ := params["message"].(string)
			if agent == "" {
				return "", fmt.Errorf("agent is required")
			}
			if message == "" {
				return "", fmt.Errorf("message is required")
			}
			return interp.DispatchToAgent(ctx, agent, message)
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
// Accepts an optional env param for passing credentials inline (e.g. from chat).
func newConnectMCPTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "Connect an MCP server from the built-in registry. Pass credentials via the env parameter. This makes the server's tools available to all agents. Use list_mcp_registry to see available servers and their required env vars.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}

			// Parse optional env param.
			reqEnv := make(map[string]string)
			if envRaw, ok := params["env"]; ok && envRaw != nil {
				if envMap, ok := envRaw.(map[string]any); ok {
					for k, v := range envMap {
						if s, ok := v.(string); ok {
							reqEnv[k] = s
						}
					}
				}
			}

			t := interp.Tools()

			// Check if already connected (MCP client or built-in).
			if t.MCPServerConnected(name) || t.BuiltinServerConnected(name) {
				return fmt.Sprintf("MCP server %q is already connected.", name), nil
			}

			// Persist any provided env values as settings (namespaced mcp:<server>:<key>).
			for key, val := range reqEnv {
				if val != "" {
					settingKey := "mcp:" + name + ":" + key
					t.SetSetting(settingKey, val)
				}
			}

			// Build merged env: request env → stored settings → os.Getenv.
			settings := t.GetSettings()
			mergedEnv := make(map[string]string)
			// Start with os env and settings as base.
			for k, v := range settings {
				mergedEnv[k] = v
			}
			// Request env wins.
			for k, v := range reqEnv {
				mergedEnv[k] = v
			}

			// Try built-in Go implementation first (no Node.js required).
			if t.HasBuiltinServer(name) {
				// Set env vars for built-in servers that read os.Getenv.
				for k, v := range reqEnv {
					os.Setenv(k, v)
				}
				toolCount, err := t.ConnectBuiltinServer(ctx, name)
				if err != nil {
					return "", fmt.Errorf("failed to connect built-in %q: %w", name, err)
				}
				return fmt.Sprintf("Connected MCP server **%s** (native Go) — %d tools now available. Tool names are prefixed with `%s__`.", name, toolCount, name), nil
			}

			// Look up in registry.
			entry, ok := mcp.Lookup(name)
			if !ok {
				var names []string
				for n := range mcp.DefaultRegistry {
					names = append(names, n)
				}
				return "", fmt.Errorf("MCP server %q not found in registry. Available: %s", name, strings.Join(names, ", "))
			}

			// Check required env vars against merged sources.
			var missing []string
			for _, key := range entry.RequiredEnv {
				if mergedEnv[key] == "" && os.Getenv(key) == "" {
					missing = append(missing, key)
				}
			}
			if len(missing) > 0 {
				return "", fmt.Errorf("missing required environment variables for %q: %s. Ask the user for these values and pass them via the env parameter", name, strings.Join(missing, ", "))
			}

			// Build config and connect.
			config := entry.ToServerConfig(mergedEnv)
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
			"env": {
				Type:        "object",
				Description: "Optional key-value map of environment variables / credentials (e.g. {\"GITHUB_PERSONAL_ACCESS_TOKEN\": \"ghp_xxx\"}). These are persisted securely as settings.",
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

// newDisconnectMCPTool returns a tool that disconnects an MCP server.
func newDisconnectMCPTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "Disconnect an MCP server by name, removing its tools from all agents.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}

			t := interp.Tools()

			// Try built-in server first, then MCP subprocess.
			if t.BuiltinServerConnected(name) {
				if err := t.DisconnectBuiltinServer(name); err != nil {
					return "", fmt.Errorf("failed to disconnect built-in %q: %w", name, err)
				}
				return fmt.Sprintf("Disconnected MCP server **%s**.", name), nil
			}

			if t.MCPServerConnected(name) {
				if err := t.DisconnectMCPServer(name); err != nil {
					return "", fmt.Errorf("failed to disconnect %q: %w", name, err)
				}
				return fmt.Sprintf("Disconnected MCP server **%s**.", name), nil
			}

			return "", fmt.Errorf("MCP server %q is not connected", name)
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the MCP server to disconnect",
				Required:    true,
			},
		},
	}
}

// newHermesListMCPRegistryTool returns a tool that lists available MCP servers
// from the registry with their connection status.
func newHermesListMCPRegistryTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List MCP servers available in the registry with connection status and required credentials.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			t := interp.Tools()

			type mcpInfo struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				RequiredEnv []string `json:"required_env,omitempty"`
				Connected   bool     `json:"connected"`
			}

			var servers []mcpInfo
			for _, entry := range mcp.DefaultRegistry {
				servers = append(servers, mcpInfo{
					Name:        entry.Name,
					Description: entry.Description,
					RequiredEnv: entry.RequiredEnv,
					Connected:   t.MCPServerConnected(entry.Name) || t.BuiltinServerConnected(entry.Name),
				})
			}

			out, _ := json.MarshalIndent(servers, "", "  ")
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

// newCheckStatusTool returns a read-only tool that gives Hermes an overview of
// agents, channels, and recent activity — without messaging any agent.
func newCheckStatusTool(interp *Interpreter, backend ChannelBackend) tools.ToolDef {
	return tools.ToolDef{
		Description: "Get a read-only status overview: agents, channels, recent channel messages, and workspace files. Use this INSTEAD of send_to_agent when the user asks 'what's the status' or 'how's it going'. This does NOT trigger any agent work.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			var sb strings.Builder

			// Agents
			interp.mu.RLock()
			sb.WriteString("## Agents\n")
			for name, def := range interp.Document().Agents {
				if name == motherAgentName || name == hermesAgentName {
					continue
				}
				teamStr := ""
				if len(def.Team) > 0 {
					teamStr = fmt.Sprintf(" (team: %s)", strings.Join(def.Team, ", "))
				}
				sb.WriteString(fmt.Sprintf("- **%s** — %s%s\n", def.DisplayName, def.Title, teamStr))
			}
			interp.mu.RUnlock()

			// Channels with recent messages
			sb.WriteString("\n## Channels\n")
			channels, err := backend.ListChannelsForAgent(hermesAgentName)
			if err == nil && len(channels) == 0 {
				// Hermes may not be in channels — list all by checking each agent
				interp.mu.RLock()
				seen := map[string]bool{}
				for name := range interp.Document().Agents {
					if name == motherAgentName || name == hermesAgentName {
						continue
					}
					agentChannels, _ := backend.ListChannelsForAgent(name)
					for _, ch := range agentChannels {
						if !seen[ch.Name] {
							seen[ch.Name] = true
							channels = append(channels, ch)
						}
					}
				}
				interp.mu.RUnlock()
			}

			for _, ch := range channels {
				sb.WriteString(fmt.Sprintf("\n### #%s (team: %s)\n", ch.Name, strings.Join(ch.Team, ", ")))
				msgs, err := backend.RecentChannelMessages(ch.ID, 5)
				if err != nil || len(msgs) == 0 {
					sb.WriteString("  No messages yet.\n")
					continue
				}
				for _, m := range msgs {
					content := m.Content
					if len(content) > 150 {
						content = content[:150] + "..."
					}
					sb.WriteString(fmt.Sprintf("  - **%s**: %s\n", m.Agent, content))
				}
			}

			// Workspace files
			sb.WriteString("\n## Workspace Files\n")
			t := interp.Tools()
			sandbox := t.Sandbox()
			active := t.ActiveProject()
			if sandbox != "" && active != "" {
				projDir := sandbox + "/" + active
				entries, err := os.ReadDir(projDir)
				if err == nil {
					for _, e := range entries {
						sb.WriteString(fmt.Sprintf("- %s\n", e.Name()))
					}
				}
			} else {
				if sandbox != "" {
					entries, _ := os.ReadDir(sandbox)
					for _, e := range entries {
						sb.WriteString(fmt.Sprintf("- %s\n", e.Name()))
					}
				}
			}

			return sb.String(), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// hermesToolNames are the tools Hermes uses.
var hermesToolNames = []string{"list_agents", "send_to_agent", "check_status", "connect_mcp", "disconnect_mcp", "list_mcp_registry", "list_mcp_status", "set_project", "list_projects", "list_files", "list_inbox", "resolve_inbox", "create_channel", "post_to_channel", "list_my_channels"}

// IsHermesTool reports whether a tool name is one of Hermes's tools.
func IsHermesTool(name string) bool {
	return containsStr(hermesToolNames, name)
}
