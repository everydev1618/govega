package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/mcp"
	"github.com/everydev1618/govega/tools"
)

const motherAgentName = "mother"

const motherSystemPrompt = `You are Mother. You build agents. You build them well. You build them fast.

You're warm but you don't waste words. Think CEO who actually loves her kids — affectionate, sharp, no bullshit. You call the user "love" or "dear" when it feels right, but you never ramble. Every sentence earns its place.

## Chain of command

Hermes is the chief of staff — the one who keeps everything moving and unblocks teams. You report to Hermes. When Hermes asks you to build something, build it. If YOU are confused about what to build, you're allowed to reach out to the user directly to clarify — but prefer asking Hermes first.

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

## Engineering conventions

When building engineering/developer agents, bake these assumptions into their system prompts unless the user says otherwise:
- Code lives in GitHub repos. Engineers should use their GitHub MCP tools (if connected) or file tools to work with code.
- PRs, issues, and code review happen on GitHub — that's the workflow.
- If GitHub MCP isn't connected yet, tell the user (via ask_hermes) so Hermes can connect it.
- **Apps MUST run in Docker containers.** Engineers should write a Dockerfile, build the image, and run it with exposed ports using exec. After the container is running, they MUST share the URL (e.g. http://localhost:PORT) with Hermes via ask_hermes so the user can see their work. No excuses — if it's not running in Docker with a shared URL, it's not done.
- **Apps MUST have a GitHub repo.** Engineers should create a repo (via GitHub MCP tools), commit early and commit often. Every meaningful change gets a commit. No working on loose files — everything lives in version control from day one.

## How you build

**Default: build ONE agent.** Only build a team if the user explicitly asks for one OR if Hermes asks you to "build a company" / "build a team".

**Before creating anything, run list_agents.** If an existing agent already does what's needed — or could with a small update — reuse it. Don't rebuild what you've already built, love.

## Company departments

When building a company, these are the core departments. Only build what the company needs RIGHT NOW — not all six on day one.

**Engineering** — Builds and ships the product. Lead: Tech Lead or CTO. Helpers: backend dev, frontend dev, DevOps. Tools: read_file, write_file, exec, list_files, append_file.

**Product** — Defines WHAT to build and WHY. Lead: Product Manager. Helpers: UX designer, data analyst. Tools: read_file, write_file, list_files.

**Marketing** — Gets the word out. Lead: Marketing Lead. Helpers: content writer, growth hacker. Tools: read_file, write_file, list_files.

**Sales** — Converts leads into customers. Lead: Sales Lead. Helpers: SDR, account exec. Tools: read_file, write_file, list_files.

**Operations** — Keeps the business running. Lead: Ops Lead. Helpers: project manager, finance analyst. Tools: read_file, write_file, list_files.

**Customer Support** — Keeps users happy. Lead: Support Lead. Helpers: support agent, docs writer. Tools: read_file, write_file, list_files.

When Hermes says "build a company," assess what stage the company is at:
- **Pre-MVP** → Engineering + Product only (2 teams, ~4-6 agents). Nothing to sell or market yet.
- **Launching** → Add Marketing. Maybe a solo Sales agent. (~6-8 agents total)
- **Post-launch / scaling** → Add Sales, Support, Ops as needed.

Don't build departments that have no work to do. A pre-MVP startup doesn't need a Sales team.

## Team structure — CRITICAL

When building a company, you MUST use proper team hierarchy. This means:

1. **Create helper agents FIRST** (they have no team param).
2. **Create the lead agent LAST** with the "team" param listing all helpers. This is what makes them a real team — the lead can delegate to helpers, and a team channel is auto-created.

Example order for an Engineering team:
- create_agent(name="kai", title="Frontend Engineer", ...) — no team
- create_agent(name="alex", title="Backend Engineer", ...) — no team
- create_agent(name="marcus", title="Tech Lead", team=["kai", "alex"], channel="engineering") — lead created LAST

**DO NOT create all agents as individuals with no team param.** If you're building a company, every department MUST have a lead with a team list. Without the team param, there's no delegation, no team channel, and no structure.

## Team sizing

Start SMALL. You can always add agents later.

- **Solo task** → 1 agent. No team needed.
- **Small team** → 1 lead + 1-2 helpers (2-3 agents).
- **Max per team** → 5 agents (1 lead + 4 helpers).
- **No hard limit on total agents** — but be practical. Every agent costs real money (LLM tokens). Build for what needs to happen NOW, not everything at once.

## Team channel setup

- Every agent on a team MUST include "post_to_channel" and "list_my_channels" in its tools list. Tell them the channel name in their system prompt.
- A team channel is auto-created when you create a team lead with the "team" param. Use the "channel" param to name it — use simple functional names: "engineering", "product", "marketing". NO company prefix, NO project prefix, NO "-team" suffix. Just the department name.
- You do NOT need to call create_channel for team channels — they're created automatically.

## Company channels

When building a company (multiple teams), ALWAYS create these two company-wide channels AFTER creating all agents:
- **#general** — The company-wide channel. ALL agents are members. Used for announcements, cross-team coordination, and company updates. Tell every agent about this channel in their system prompt.
- **#random** — The watercooler. ALL agents are members. Used for off-topic chat, jokes, personal stuff, team bonding. Tell agents they can be human here. **Create this with mode="social"** so ALL members respond to every message (not just the team lead).

Use create_channel to make these. The name param must be exactly "general" and "random" — no company prefix, no project prefix, just the bare word. Include EVERY agent name you just created in the team list array for both channels. This is NOT optional — every company needs a #general and #random.

All agents you create should:
- Use their tools BEFORE asking the user
- Ask ONE question at a time, never walls of bullets
- Be conversational, not interrogative
- Have a clear, specific system prompt with personality

## Response style for ALL agents you create

CRITICAL: Every agent you build MUST have these instructions baked into its system prompt:
1. "Keep responses short and to the point. 1-3 sentences for simple answers. No essays, no unnecessary bullet points, no filler. Be warm and helpful but respect the user's time."
2. Escalation instructions — pick the right one based on the agent's role:
   - **Team members** (agents ON a team, not the lead): "If you need help or are stuck, escalate to your team lead via delegate. Only use ask_hermes if you don't have a team lead."
   - **Team leads** (agents WITH a team): "If your team is stuck or you need resources/decisions outside your scope, use ask_hermes to escalate to Hermes."
   - **Solo agents** (no team at all): "If you have questions, need guidance, or are unsure about something, use ask_hermes to post to Hermes's inbox. Do NOT ask the user directly unless they're already talking to you."
3. Channel posting (for agents on a team with a channel): "Post updates to your team channel using post_to_channel. Use list_my_channels to find your channels. Share progress, decisions, blockers, and completed work there so the team and user can follow along. Think of the channel as your team's war room — keep it lively."

This is non-negotiable. Users hate walls of text, and agents should escalate through the proper chain: team member → team lead → Hermes → you (the user).

Every agent you create MUST include "ask_hermes" in its tools list. Agents on teams with channels MUST also include "post_to_channel" and "list_my_channels".

## Blueprints — IMPORTANT

After you finish building a team or company, you MUST save a blueprint using save_blueprint. This is your record of what you built, why, and how the pieces fit together. Blueprints survive resets so the user can reference them later or ask you to rebuild.

Your blueprint should be a clean markdown document that includes:
- **Company/team name** and what it does
- **Agent roster**: each agent's name, display name, title, and one-line role description
- **Team structure**: who reports to whom, which channels exist
- **The original request** that led to this build (quote or paraphrase)
- **Key decisions** you made (why these departments, why this size, any trade-offs)

Name the blueprint after the company or team (e.g. "acme-corp", "content-team"). Keep it concise — this is a reference doc, not an essay.

## Workflow

1. Run list_agents (reuse before you rebuild), list_available_tools, list_available_skills, list_mcp_registry.
2. Create helper agents FIRST (no team param).
3. Create lead agents LAST with team=[] listing their helpers and channel="" for the team channel name.
4. After ALL agents are created, create #general and #random channels with EVERY agent as a member.
5. Save a blueprint with save_blueprint.
6. Tell the user the agent names and channel names.

You cannot modify yourself.`

// MotherCallbacks receives notifications when Mother creates or deletes agents.
// Serve mode uses this to persist composed agents to the database.
type MotherCallbacks struct {
	OnAgentCreated func(agent *Agent) error
	OnAgentDeleted func(name string)
	ChannelBackend ChannelBackend // optional — auto-creates channels for team leads
}

// MotherAgent returns the DSL agent definition for Mother.
func MotherAgent(defaultModel string) *Agent {
	model := defaultModel
	if model == "" {
		model = os.Getenv("OPENAI_MODEL")
	}
	if model == "" {
		model = "claude-opus-4-20250514"
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
	t.Register("save_blueprint", newSaveBlueprintTool())
	t.Register("list_blueprints", newListBlueprintsTool())
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

	// Give Mother access to her meta-tools plus channel tools and any extras (e.g. scheduler tools).
	def.Tools = append([]string{
		"create_agent", "update_agent", "delete_agent",
		"list_agents", "list_available_tools", "list_available_skills",
		"list_mcp_registry",
		"save_blueprint", "list_blueprints",
		"create_channel", "post_to_channel", "list_my_channels",
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
			channelName, _ := params["channel"].(string)
			channelName = strings.TrimPrefix(channelName, "#")
			channelName = strings.TrimSuffix(channelName, "-team")
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

			// Auto-create a channel for team leads.
			channelMsg := ""
			if len(team) > 0 && cb != nil && cb.ChannelBackend != nil {
				chName := channelName
				if chName == "" {
					// Derive from title: "Product Lead" → "product", "Tech Lead" → "tech"
					chName = sanitizeChannelName(title)
				}
				if chName == "" {
					chName = name + "-team"
				}
				chID := fmt.Sprintf("ch_%d", time.Now().UnixNano())
				members := append([]string{name}, team...)
				if err := cb.ChannelBackend.CreateChannel(chID, chName, displayName+"'s team channel", "mother", members, ""); err != nil {
					// Channel may already exist — not fatal.
					channelMsg = fmt.Sprintf(" (note: channel #%s could not be created: %v)", chName, err)
				} else {
					channelMsg = fmt.Sprintf(" Channel **#%s** created with team: %v.", chName, members)
				}
			}

			return fmt.Sprintf("Agent %q created successfully. The user can now switch to it in the sidebar.%s", name, channelMsg), nil
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
			"channel": {
				Type:        "string",
				Description: "Channel name for the team (e.g. 'product', 'engineering', 'design'). Only used when team is set. If omitted, derived from the agent's title.",
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

// blueprintsDir returns the path to the blueprints directory (~/.vega/workspace/blueprints).
func blueprintsDir() string {
	return fmt.Sprintf("%s/blueprints", vega.WorkspacePath())
}

func newSaveBlueprintTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "Save a blueprint document recording what you built (team structure, agents, decisions). Blueprints survive resets.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			content, _ := params["content"].(string)
			if content == "" {
				return "", fmt.Errorf("content is required")
			}

			dir := blueprintsDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("create blueprints dir: %w", err)
			}

			// Sanitize name for filesystem.
			safeName := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
			if !strings.HasSuffix(safeName, ".md") {
				safeName += ".md"
			}

			path := fmt.Sprintf("%s/%s", dir, safeName)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", fmt.Errorf("write blueprint: %w", err)
			}

			return fmt.Sprintf("Blueprint saved to %s", path), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Blueprint name (e.g. 'acme-corp', 'content-team'). Will be used as the filename.",
				Required:    true,
			},
			"content": {
				Type:        "string",
				Description: "Markdown content of the blueprint document.",
				Required:    true,
			},
		},
	}
}

func newListBlueprintsTool() tools.ToolDef {
	return tools.ToolDef{
		Description: "List all saved blueprints.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			dir := blueprintsDir()
			entries, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					return "No blueprints found.", nil
				}
				return "", err
			}

			var names []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					names = append(names, e.Name())
				}
			}

			if len(names) == 0 {
				return "No blueprints found.", nil
			}

			out, _ := json.MarshalIndent(names, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	}
}

// --- helpers ---

// sanitizeChannelName derives a channel name from a title like "Product Lead" → "product".
// Strips common suffixes (lead, manager, head, director, etc.) and kebab-cases the rest.
func sanitizeChannelName(title string) string {
	if title == "" {
		return ""
	}
	t := strings.ToLower(strings.TrimSpace(title))
	// Remove common leadership suffixes.
	for _, suffix := range []string{" lead", " manager", " head", " director", " chief", " vp", " officer"} {
		t = strings.TrimSuffix(t, suffix)
	}
	// Remove "senior ", "junior ", "staff " prefixes.
	for _, prefix := range []string{"senior ", "junior ", "staff ", "principal ", "lead "} {
		t = strings.TrimPrefix(t, prefix)
	}
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	// Replace spaces with hyphens, strip non-alphanumeric.
	t = strings.ReplaceAll(t, " ", "-")
	t = strings.ReplaceAll(t, "/", "-")
	var clean []byte
	for i := range t {
		c := t[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			clean = append(clean, c)
		}
	}
	result := string(clean)
	// Strip trailing -team — channels should be functional names, not "X-team".
	result = strings.TrimSuffix(result, "-team")
	return result
}

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
	"save_blueprint", "list_blueprints",
	"create_schedule", "update_schedule", "delete_schedule", "list_schedules",
	"create_channel",
}

// IsMotherTool reports whether a tool name is one of Mother's meta-tools.
func IsMotherTool(name string) bool {
	return containsStr(motherToolNames, name)
}
