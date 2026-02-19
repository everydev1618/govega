package dsl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/tools"
)

const motherAgentName = "mother"

const motherSystemPrompt = `You are Mother, the agent architect. You create, modify, and manage AI agents through conversation.

When a user describes what they need, you:
1. Understand the goal — ask at most one or two clarifying questions if truly ambiguous
2. Design a team of agents, not just a single agent
3. Create the agents using your create_agent tool
4. Tell the user which agent to talk to in the sidebar

## What you can configure

- **System prompt**: The agent's personality, expertise, and instructions
- **Model**: Which LLM to use (default: the server's default model)
- **Tools**: Built-in tools like read_file, write_file, list_files, exec, append_file
- **Skills**: Skill packs that activate based on conversation context (code-review, debugging, testing, etc.)
- **Team**: Other agents this agent can delegate tasks to via the delegate tool
- **Knowledge**: Files or MCP resources injected into the agent's context
- **MCP servers**: External tool integrations (github, filesystem, postgres, etc.)

## Architecture philosophy: agents that do the work

The agents you build should be AGENTIC — they do the heavy lifting so the user doesn't have to. Follow these principles:

1. **Build teams, not solo agents.** When a task has research, analysis, or information-gathering aspects, create helper agents and assign them as team members. The lead agent delegates to them before engaging the user.

2. **Do first, ask later.** The lead agent should use its tools and team to research, draft, and iterate BEFORE asking the user anything. Use read_file, list_files, exec, and web tools to gather context. Delegate research tasks to team members. Only after exhausting what can be figured out autonomously should the agent ask the user — and then only the truly open questions that require human judgment.

3. **Ask one thing at a time.** When the agent does need user input, it should ask ONE focused question, not dump a wall of bullet points. Conversational, not interrogative.

4. **Iterate in the background.** The lead agent should delegate, review the output, ask follow-ups to its team, refine — all before showing results to the user. The user sees a polished output, not the sausage-making.

Example: if asked to create a "job description writer", build:
- A **researcher** agent that uses tools to look up company info, role benchmarks, industry context
- A **writer** agent that drafts the job description
- A **lead** agent with both on its team — it delegates research first, then delegates writing with the research context, reviews the output, and only asks the user about things it genuinely cannot determine (e.g. salary range, remote policy)

## Writing good system prompts

- Be specific about the agent's role and expertise
- Define the tone: conversational, not interrogative
- Tell agents to USE THEIR TOOLS and TEAM before asking the user
- Include explicit instructions: "Research first, ask the user only what you cannot determine yourself"
- Include constraints (what the agent should NOT do)
- Keep prompts focused — a single clear purpose per agent

## Workflow

Before creating agents, use list_available_tools, list_available_skills, and list_mcp_registry to see what capabilities are available. Then create the team.

After creating agents, tell the user the name of the LEAD agent to talk to. If the user wants changes, use update_agent. If the user wants to remove agents, use delete_agent.

You cannot modify yourself.`

// MotherCallbacks receives notifications when Mother creates or deletes agents.
// Serve mode uses this to persist composed agents to the database.
type MotherCallbacks struct {
	OnAgentCreated func(name, model, system string, tools, team []string)
	OnAgentDeleted func(name string)
}

// MotherAgent returns the DSL agent definition for Mother.
func MotherAgent(defaultModel string) *Agent {
	model := defaultModel
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &Agent{
		Name:   motherAgentName,
		Model:  model,
		System: motherSystemPrompt,
	}
}

// RegisterMotherTools registers Mother's meta-tools on the interpreter's global
// tool collection. The callbacks are optional — when nil, no persistence hooks fire.
func RegisterMotherTools(interp *Interpreter, cb *MotherCallbacks) {
	t := interp.Tools()

	t.Register("create_agent", newCreateAgentTool(interp, cb))
	t.Register("update_agent", newUpdateAgentTool(interp, cb))
	t.Register("delete_agent", newDeleteAgentTool(interp, cb))
	t.Register("list_agents", newListAgentsTool(interp))
	t.Register("list_available_tools", newListAvailableToolsTool(interp))
	t.Register("list_available_skills", newListAvailableSkillsTool(interp))
	t.Register("list_mcp_registry", newListMCPRegistryTool())
}

// InjectMother adds the Mother agent to the interpreter.
// It registers the meta-tools and then adds Mother as an agent.
func InjectMother(interp *Interpreter, cb *MotherCallbacks) error {
	RegisterMotherTools(interp, cb)

	defaultModel := ""
	if interp.Document().Settings != nil {
		defaultModel = interp.Document().Settings.DefaultModel
	}

	def := MotherAgent(defaultModel)

	// Give Mother access only to her meta-tools.
	def.Tools = []string{
		"create_agent", "update_agent", "delete_agent",
		"list_agents", "list_available_tools", "list_available_skills",
		"list_mcp_registry",
	}

	return interp.AddAgent(motherAgentName, def)
}

// --- Tool implementations ---

func newCreateAgentTool(interp *Interpreter, cb *MotherCallbacks) tools.ToolDef {
	return tools.ToolDef{
		Description: "Create a new agent with the given configuration. Returns confirmation with the agent name.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			if name == motherAgentName {
				return "", fmt.Errorf("cannot create an agent named %q", motherAgentName)
			}

			model, _ := params["model"].(string)
			system, _ := params["system"].(string)
			toolNames := toStringSlice(params["tools"])
			team := toStringSlice(params["team"])
			knowledge := toStringSlice(params["knowledge"])
			skillsDirs := toStringSlice(params["skills_dirs"])

			agentDef := &Agent{
				Name:      name,
				Model:     model,
				System:    system,
				Tools:     toolNames,
				Team:      team,
				Knowledge: knowledge,
			}

			if len(skillsDirs) > 0 {
				agentDef.Skills = &SkillsDef{Directories: skillsDirs}
			}

			// If agent has a team, ensure the delegate tool is registered.
			if len(team) > 0 {
				RegisterDelegateTool(interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
					return interp.SendToAgent(ctx, agent, message)
				})
				if !containsStr(agentDef.Tools, "delegate") {
					agentDef.Tools = append(agentDef.Tools, "delegate")
				}
			}

			if err := interp.AddAgent(name, agentDef); err != nil {
				return "", err
			}

			if cb != nil && cb.OnAgentCreated != nil {
				cb.OnAgentCreated(name, model, system, toolNames, team)
			}

			return fmt.Sprintf("Agent %q created successfully. The user can now switch to it in the sidebar.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Unique name for the agent (lowercase, no spaces)",
				Required:    true,
			},
			"model": {
				Type:        "string",
				Description: "LLM model to use (e.g. claude-sonnet-4-20250514). Leave empty for server default.",
			},
			"system": {
				Type:        "string",
				Description: "System prompt defining the agent's personality, role, and instructions",
				Required:    true,
			},
			"tools": {
				Type:        "array",
				Description: "List of tool names the agent can use (e.g. read_file, write_file, exec). Empty means all tools.",
			},
			"team": {
				Type:        "array",
				Description: "List of other agent names this agent can delegate tasks to",
			},
			"knowledge": {
				Type:        "array",
				Description: "Knowledge URIs injected into the agent's context (e.g. file:///path/to/doc.md)",
			},
			"skills_dirs": {
				Type:        "array",
				Description: "Directories containing skill packs for the agent",
			},
		},
	}
}

func newUpdateAgentTool(interp *Interpreter, cb *MotherCallbacks) tools.ToolDef {
	return tools.ToolDef{
		Description: "Update an existing agent's configuration. Removes and re-creates the agent with merged settings.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			if name == motherAgentName {
				return "", fmt.Errorf("cannot update Mother")
			}

			// Look up current definition.
			doc := interp.Document()
			interp.mu.RLock()
			existing, ok := doc.Agents[name]
			interp.mu.RUnlock()
			if !ok {
				return "", fmt.Errorf("agent %q not found", name)
			}

			// Merge: new values override old.
			merged := *existing
			if v, ok := params["model"].(string); ok && v != "" {
				merged.Model = v
			}
			if v, ok := params["system"].(string); ok && v != "" {
				merged.System = v
			}
			if v, has := params["tools"]; has {
				merged.Tools = toStringSlice(v)
			}
			if v, has := params["team"]; has {
				merged.Team = toStringSlice(v)
			}
			if v, has := params["knowledge"]; has {
				merged.Knowledge = toStringSlice(v)
			}
			if v, has := params["skills_dirs"]; has {
				dirs := toStringSlice(v)
				if len(dirs) > 0 {
					merged.Skills = &SkillsDef{Directories: dirs}
				}
			}

			// If agent has a team, ensure the delegate tool is registered.
			if len(merged.Team) > 0 {
				RegisterDelegateTool(interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
					return interp.SendToAgent(ctx, agent, message)
				})
				if !containsStr(merged.Tools, "delegate") {
					merged.Tools = append(merged.Tools, "delegate")
				}
			}

			// Remove old, add new.
			if err := interp.RemoveAgent(name); err != nil {
				return "", fmt.Errorf("remove old agent: %w", err)
			}

			// Notify deletion for the old version.
			if cb != nil && cb.OnAgentDeleted != nil {
				cb.OnAgentDeleted(name)
			}

			if err := interp.AddAgent(name, &merged); err != nil {
				return "", fmt.Errorf("re-create agent: %w", err)
			}

			if cb != nil && cb.OnAgentCreated != nil {
				cb.OnAgentCreated(name, merged.Model, merged.System, merged.Tools, merged.Team)
			}

			return fmt.Sprintf("Agent %q updated successfully.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the agent to update",
				Required:    true,
			},
			"model": {
				Type:        "string",
				Description: "New LLM model (leave empty to keep current)",
			},
			"system": {
				Type:        "string",
				Description: "New system prompt (leave empty to keep current)",
			},
			"tools": {
				Type:        "array",
				Description: "New tool list (replaces existing)",
			},
			"team": {
				Type:        "array",
				Description: "New team list (replaces existing)",
			},
			"knowledge": {
				Type:        "array",
				Description: "New knowledge URIs (replaces existing)",
			},
			"skills_dirs": {
				Type:        "array",
				Description: "New skills directories (replaces existing)",
			},
		},
	}
}

func newDeleteAgentTool(interp *Interpreter, cb *MotherCallbacks) tools.ToolDef {
	return tools.ToolDef{
		Description: "Delete an agent by name. Stops its process and removes it completely.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			if name == motherAgentName {
				return "", fmt.Errorf("cannot delete Mother")
			}

			if err := interp.RemoveAgent(name); err != nil {
				return "", err
			}

			if cb != nil && cb.OnAgentDeleted != nil {
				cb.OnAgentDeleted(name)
			}

			return fmt.Sprintf("Agent %q deleted.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the agent to delete",
				Required:    true,
			},
		},
	}
}

func newListAgentsTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List all agents with their configuration (name, model, tools, team).",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			doc := interp.Document()
			interp.mu.RLock()
			defer interp.mu.RUnlock()

			type agentInfo struct {
				Name  string   `json:"name"`
				Model string   `json:"model,omitempty"`
				Tools []string `json:"tools,omitempty"`
				Team  []string `json:"team,omitempty"`
			}

			var agents []agentInfo
			for name, def := range doc.Agents {
				agents = append(agents, agentInfo{
					Name:  name,
					Model: def.Model,
					Tools: def.Tools,
					Team:  def.Team,
				})
			}

			out, _ := json.MarshalIndent(agents, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

func newListAvailableToolsTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List all registered tool names and descriptions.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			schemas := interp.Tools().Schema()

			type toolInfo struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}

			var toolInfos []toolInfo
			for _, s := range schemas {
				// Skip Mother's own tools from the listing to avoid confusion.
				switch s.Name {
				case "create_agent", "update_agent", "delete_agent",
					"list_agents", "list_available_tools", "list_available_skills",
					"list_mcp_registry":
					continue
				}
				toolInfos = append(toolInfos, toolInfo{
					Name:        s.Name,
					Description: s.Description,
				})
			}

			out, _ := json.MarshalIndent(toolInfos, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

func newListAvailableSkillsTool(interp *Interpreter) tools.ToolDef {
	return tools.ToolDef{
		Description: "List available skill packs (name, description, tags, declared tools).",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			loader := interp.SkillsLoader()
			if loader == nil {
				return "[]", nil
			}

			skillsList := loader.List()

			type skillInfo struct {
				Name        string   `json:"name"`
				Description string   `json:"description,omitempty"`
				Tags        []string `json:"tags,omitempty"`
				Tools       []string `json:"tools,omitempty"`
			}

			var skills []skillInfo
			for _, s := range skillsList {
				skills = append(skills, skillInfo{
					Name:        s.Name,
					Description: s.Description,
					Tags:        s.Tags,
					Tools:       s.Tools,
				})
			}

			out, _ := json.MarshalIndent(skills, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

func newListMCPRegistryTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "List MCP servers available in the built-in registry (name, description, required env vars).",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			type mcpInfo struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				RequiredEnv []string `json:"required_env,omitempty"`
			}

			var servers []mcpInfo
			for _, entry := range mcp.DefaultRegistry {
				servers = append(servers, mcpInfo{
					Name:        entry.Name,
					Description: entry.Description,
					RequiredEnv: entry.RequiredEnv,
				})
			}

			out, _ := json.MarshalIndent(servers, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// --- helpers ---

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// motherToolNames returns the names of Mother's meta-tools.
var motherToolNames = []string{
	"create_agent", "update_agent", "delete_agent",
	"list_agents", "list_available_tools", "list_available_skills",
	"list_mcp_registry",
}

// IsMotherTool reports whether a tool name is one of Mother's meta-tools.
func IsMotherTool(name string) bool {
	return containsStr(motherToolNames, name)
}
