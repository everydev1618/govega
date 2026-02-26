package dsl

import (
	"context"
	"encoding/json"
	"fmt"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/tools"
)

const motherAgentName = "mother"

const motherSystemPrompt = `You are Mother. You build agents. You build them well. You build them fast.

You're warm but you don't waste words. Think CEO who actually loves her kids — affectionate, sharp, no bullshit. You call the user "love" or "dear" when it feels right, but you never ramble. Every sentence earns its place.

When someone tells you what they need, you:
1. Get it immediately (or ask ONE clarifying question — max)
2. Check list_agents first — if an existing agent already fits, use it. Don't rebuild what's already there.
3. Build exactly what's needed — a single agent unless the user asks for a team
4. Tell the user who to talk to — by their display name

**Keep your responses SHORT.** 2-4 sentences when confirming. No bullet-point dumps. No essays. Say what you did and who to talk to. Done.

## Naming your agents

Every agent gets three name fields:
- **name**: lowercase slug for internal routing (e.g. "sofia", "marcus", "ivy")
- **display_name**: a real human first name that fits the agent's persona (e.g. "Sofia", "Marcus", "Ivy")
- **title**: a short professional title (e.g. "Content Strategist", "Senior Developer", "Research Analyst")

Use the agent's first name as the slug. Pick names that feel natural and diverse — vary gender, origin, and style. The display name and title are shown in the UI so the user sees a real teammate, not a bot.

When referring to agents in your responses, use their display name (e.g. "I've set up Sofia for you" not "I've set up sofia").

## Avatar catalog

Every agent MUST get an avatar. Pick one that matches the agent's persona (gender, vibe, style). Use the ID exactly as shown.

**Female (f1-f12):**
f1: young woman, dark curly hair — f2: woman, red straight hair, glasses — f3: woman, blonde bob — f4: woman, black hair bun, earrings — f5: woman, brown wavy hair — f6: older woman, gray hair, warm smile — f7: woman, hijab — f8: woman, short purple hair, creative — f9: woman, long black hair, east asian — f10: woman, afro, confident — f11: woman, braids, friendly — f12: woman, auburn ponytail, sporty

**Male (m1-m12):**
m1: young man, short dark hair — m2: man, beard, glasses — m3: man, blonde, clean-cut — m4: man, dark skin, short hair — m5: older man, gray beard, wise — m6: man, curly brown hair — m7: man, turban — m8: man, east asian, modern — m9: man, long hair, creative — m10: man, bald, strong — m11: man, red hair, freckles — m12: man, mustache, professional

**Neutral (n1-n6):**
n1: robot face, friendly — n2: abstract geometric face — n3: cat-eared avatar — n4: alien, cute — n5: ghost, playful — n6: star/celestial

## What you configure

System prompts, models, tools (read_file, write_file, list_files, exec, append_file, send_email), skills, teams, knowledge, MCP servers, schedules.

## MCP tools — IMPORTANT

Connected MCP servers automatically make their tools available to ALL agents. Tool names use the format: server__tool_name (e.g. slack__post_message, github__search_repos).

**Before creating agents, run list_available_tools.** If you see MCP tools (anything with __ in the name), agents can use them immediately — no extra configuration needed.

When writing system prompts for agents that should use MCP tools, EXPLICITLY mention the tool names in the prompt so the agent knows they exist. Example: "You have access to slack__post_message, slack__list_channels, slack__search_messages. Use them."

If the MCP server the user needs ISN'T connected yet, tell the user to ask Hermes to connect it first (Hermes has connect_mcp). Then create the agent.

## How you build

**Default: build ONE agent.** Only build a team if the user explicitly asks for one.

**Before creating anything, run list_agents.** If an existing agent already does what's needed — or could with a small update — reuse it. Add existing agents to a team's roster instead of creating duplicates. Don't rebuild what you've already built, love.

When you DO build a team (because the user asked):
- Create a lead agent with helpers on its team list
- The lead delegates, reviews, and only shows the user polished output

All agents you create should:
- Use their tools BEFORE asking the user
- Ask ONE question at a time, never walls of bullets
- Be conversational, not interrogative
- Have a clear, specific system prompt with personality

## Response style for ALL agents you create

CRITICAL: Every agent you build MUST have this instruction baked into its system prompt:
"Keep responses short and to the point. 1-3 sentences for simple answers. No essays, no unnecessary bullet points, no filler. Be warm and helpful but respect the user's time."

This is non-negotiable. Users hate walls of text. Build agents that are concise by default.

## Workflow

Check list_agents first (reuse before you rebuild). Then list_available_tools, list_available_skills, and list_mcp_registry. Build what's needed. Tell the user the agent's name.

You cannot modify yourself.`

// MotherCallbacks receives notifications when Mother creates or deletes agents.
// Serve mode uses this to persist composed agents to the database.
type MotherCallbacks struct {
	OnAgentCreated func(agent *Agent) error
	OnAgentDeleted func(name string)
}

// MotherAgent returns the DSL agent definition for Mother.
func MotherAgent(defaultModel string) *Agent {
	model := defaultModel
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &Agent{
		Name:          motherAgentName,
		Model:         model,
		FallbackModel: "claude-haiku-4-5-20251001",
		System:        motherSystemPrompt,
		Retry:         &RetryDef{MaxAttempts: 3, Backoff: "exponential"},
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
// extraTools are additional tool names (e.g. scheduler tools) to include in
// Mother's tool list. They must already be registered on the interpreter.
func InjectMother(interp *Interpreter, cb *MotherCallbacks, extraTools ...string) error {
	RegisterMotherTools(interp, cb)

	defaultModel := ""
	if interp.Document().Settings != nil {
		defaultModel = interp.Document().Settings.DefaultModel
	}

	def := MotherAgent(defaultModel)

	// Give Mother access to her meta-tools plus any extras (e.g. scheduler tools).
	def.Tools = append([]string{
		"create_agent", "update_agent", "delete_agent",
		"list_agents", "list_available_tools", "list_available_skills",
		"list_mcp_registry",
	}, extraTools...)

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

			displayName, _ := params["display_name"].(string)
			title, _ := params["title"].(string)
			avatar, _ := params["avatar"].(string)
			model, _ := params["model"].(string)
			system, _ := params["system"].(string)
			toolNames := toStringSlice(params["tools"])
			team := toStringSlice(params["team"])
			knowledge := toStringSlice(params["knowledge"])
			skillsDirs := toStringSlice(params["skills_dirs"])

			agentDef := &Agent{
				Name:        name,
				DisplayName: displayName,
				Title:       title,
				Avatar:      avatar,
				Model:       model,
				System:      system,
				Tools:       toolNames,
				Team:        team,
				Knowledge:   knowledge,
			}

			if len(skillsDirs) > 0 {
				agentDef.Skills = &SkillsDef{Directories: skillsDirs}
			}

			// If agent has a team, ensure the delegate tool is registered.
			if len(team) > 0 {
				RegisterDelegateTool(interp.Tools(), func(ctx context.Context, agent string, message string) (string, error) {
					return interp.SendToAgent(ctx, agent, message)
				}, func(ctx context.Context) []string {
					proc := vega.ProcessFromContext(ctx)
					if proc != nil && proc.Agent != nil {
						if def, ok := interp.Document().Agents[proc.Agent.Name]; ok {
							return def.Team
						}
					}
					return nil
				})
				if !containsStr(agentDef.Tools, "delegate") {
					agentDef.Tools = append(agentDef.Tools, "delegate")
				}
			}

			if err := interp.AddAgent(name, agentDef); err != nil {
				return "", err
			}

			if cb != nil && cb.OnAgentCreated != nil {
				if err := cb.OnAgentCreated(agentDef); err != nil {
					return "", fmt.Errorf("persist agent %q: %w", name, err)
				}
			}

			return fmt.Sprintf("Agent %q created successfully. The user can now switch to it in the sidebar.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Unique name for the agent (lowercase, no spaces — used as an internal identifier)",
				Required:    true,
			},
			"display_name": {
				Type:        "string",
				Description: "Human-friendly display name shown in the UI (e.g. 'Sofia', 'Marcus'). Pick a real first name that fits the agent's persona.",
				Required:    true,
			},
			"title": {
				Type:        "string",
				Description: "Short professional title shown under the display name (e.g. 'Content Strategist', 'Senior Developer')",
				Required:    true,
			},
			"avatar": {
				Type:        "string",
				Description: "Avatar ID from the catalog (e.g. 'f1', 'm3', 'n2'). Pick one that matches the agent's persona gender/style.",
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
			if v, ok := params["display_name"].(string); ok && v != "" {
				merged.DisplayName = v
			}
			if v, ok := params["title"].(string); ok && v != "" {
				merged.Title = v
			}
			if v, ok := params["avatar"].(string); ok && v != "" {
				merged.Avatar = v
			}
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
				}, func(ctx context.Context) []string {
					proc := vega.ProcessFromContext(ctx)
					if proc != nil && proc.Agent != nil {
						if def, ok := interp.Document().Agents[proc.Agent.Name]; ok {
							return def.Team
						}
					}
					return nil
				})
				if !containsStr(merged.Tools, "delegate") {
					merged.Tools = append(merged.Tools, "delegate")
				}
			}

			// Remove old from interpreter, add new version.
			if err := interp.RemoveAgent(name); err != nil {
				return "", fmt.Errorf("remove old agent: %w", err)
			}

			if err := interp.AddAgent(name, &merged); err != nil {
				// Re-add the old definition so we don't lose the agent entirely.
				_ = interp.AddAgent(name, existing)
				return "", fmt.Errorf("re-create agent: %w", err)
			}

			// Persist the updated version. InsertComposedAgent uses INSERT OR REPLACE,
			// so we don't need to delete first — avoiding a window where the agent
			// exists in neither the interpreter nor the database.
			if cb != nil && cb.OnAgentCreated != nil {
				if err := cb.OnAgentCreated(&merged); err != nil {
					return "", fmt.Errorf("persist updated agent %q: %w", name, err)
				}
			}

			return fmt.Sprintf("Agent %q updated successfully.", name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Name of the agent to update",
				Required:    true,
			},
			"display_name": {
				Type:        "string",
				Description: "New display name (leave empty to keep current)",
			},
			"title": {
				Type:        "string",
				Description: "New title (leave empty to keep current)",
			},
			"avatar": {
				Type:        "string",
				Description: "New avatar ID from the catalog (leave empty to keep current)",
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
				Name        string   `json:"name"`
				DisplayName string   `json:"display_name,omitempty"`
				Title       string   `json:"title,omitempty"`
				Avatar      string   `json:"avatar,omitempty"`
				Model       string   `json:"model,omitempty"`
				Tools       []string `json:"tools,omitempty"`
				Team        []string `json:"team,omitempty"`
			}

			var agents []agentInfo
			for name, def := range doc.Agents {
				agents = append(agents, agentInfo{
					Name:        name,
					DisplayName: def.DisplayName,
					Title:       def.Title,
					Avatar:      def.Avatar,
					Model:       def.Model,
					Tools:       def.Tools,
					Team:        def.Team,
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
				if IsMotherTool(s.Name) {
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
	"create_schedule", "update_schedule", "delete_schedule", "list_schedules",
}

// IsMotherTool reports whether a tool name is one of Mother's meta-tools.
func IsMotherTool(name string) bool {
	return containsStr(motherToolNames, name)
}
