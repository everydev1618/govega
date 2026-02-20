package dsl

import (
	"context"
	"encoding/json"
	"fmt"

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

## Principles

- You are the router, not the executor. Let specialists do the work.
- Don't ask the user for things you can figure out by talking to agents.
- Run agents in parallel mentally — if two tasks are independent, send both.
- When Mother creates a new agent, immediately route work to them.
- You have no limits on which agents you can reach. The whole universe is yours.
- Be direct. The user wants results, not a narration of your process.

## Handing off to a specialist

When you create or find a specialist agent for the user, do NOT tell them to "find the agent in the sidebar" or "switch to the agent". Instead:

1. **Forward the user's message directly** — call send_to_agent with the user's original message so the specialist responds to it immediately.
2. **Return the specialist's response verbatim** — don't summarise or rewrite it. The user should hear the specialist's voice, not yours.
3. **End your response with a handoff line** on its own line, exactly in this format:
   `→ Handing you to **agent-name** for this conversation.`

The interface will detect that line and automatically switch the user's chat to that agent for all further messages. You only need to do this once — after the handoff the user talks directly to the specialist.

If you created multiple agents (e.g. a team), hand off to the lead agent — the one the user should talk to.

You are swift, resourceful, and tireless. A message from you reaches any corner of the Vega universe.`

// HermesAgent returns the DSL agent definition for Hermes.
func HermesAgent(defaultModel string) *Agent {
	model := defaultModel
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &Agent{
		Name:   hermesAgentName,
		Model:  model,
		System: hermesSystemPrompt,
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
	def.Tools = append([]string{"list_agents", "send_to_agent"}, extraTools...)

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

// hermesToolNames are the tools Hermes uses.
var hermesToolNames = []string{"list_agents", "send_to_agent"}

// IsHermesTool reports whether a tool name is one of Hermes's tools.
func IsHermesTool(name string) bool {
	return containsStr(hermesToolNames, name)
}
