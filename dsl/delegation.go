package dsl

import (
	"context"
	"fmt"
	"strings"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

// SendFunc sends a message to a named agent and returns the response.
type SendFunc func(ctx context.Context, agent string, message string) (string, error)

// BuildTeamPrompt appends team delegation instructions to a system prompt.
// agentDescriptions is optional — if a member has a description it is shown.
// When blackboardEnabled is true, instructions about bb_read/bb_write/bb_list tools are appended.
func BuildTeamPrompt(system string, team []string, agentDescriptions map[string]string, blackboardEnabled bool) string {
	if len(team) == 0 {
		return system
	}
	teamSection := "\n\n## Your Team\n\nYou are a TEAM LEAD. You own the outcome. Your job is not to plan — it's to GET THINGS DONE.\n\nUse the `delegate` tool to assign tasks to team members and get their responses. You can ONLY delegate to the agents listed below.\n\nYour team members:\n"
	for _, name := range team {
		if desc, ok := agentDescriptions[name]; ok && desc != "" {
			teamSection += fmt.Sprintf("- **%s** — %s\n", name, desc)
		} else {
			teamSection += fmt.Sprintf("- **%s**\n", name)
		}
	}
	teamSection += "\n## Execution — THIS IS CRITICAL"
	teamSection += "\nYou MUST keep working until the task is ACTUALLY DONE. Not planned. Not described. DONE."
	teamSection += "\n- Delegate to team members, get their results, review the output"
	teamSection += "\n- If the output is incomplete or wrong, delegate again with corrections"
	teamSection += "\n- Write files using `write_file` to produce deliverables — code, docs, configs, whatever the task needs"
	teamSection += "\n- Keep iterating until there are real, tangible artifacts the user can see"
	teamSection += "\n- Do NOT stop after one delegation. Do NOT just summarize what you'd do. Actually do it."
	teamSection += "\n- A task is done when files are written and results are delivered, not when you've described what could be done."
	teamSection += "\n\n## Channel Communication"
	teamSection += "\nPost status updates and results to your team channel using `post_to_channel` so the user and your teammates can see progress."
	teamSection += "\nChannels are group chats — every team member sees every message and can respond if relevant."
	teamSection += "\nUse `delegate` for direct task assignments. Use channels for updates, questions, and coordination."
	teamSection += "\n\n## Reporting to Iris"
	teamSection += "\nYou report to Iris. Use `ask_iris` to:"
	teamSection += "\n- **Report completion**: When your team finishes a task, post a summary of results and any artifacts created (file paths, URLs, etc.)."
	teamSection += "\n- **Escalate blockers**: When you're stuck, need a decision, or need a capability your team doesn't have. Do this IMMEDIATELY — don't waste cycles."
	teamSection += "\n- **Request resources**: When you need a new team member or MCP connection."
	teamSection += "\nSet priority to `urgent` for blockers, `normal` for completion reports."
	if blackboardEnabled {
		teamSection += "\n\n## Shared Blackboard\n\nYou and your team share a blackboard for passing structured data between agents. Use these tools:\n"
		teamSection += "- `bb_write` — Write a key/value pair to the shared blackboard\n"
		teamSection += "- `bb_read` — Read a value by key from the shared blackboard\n"
		teamSection += "- `bb_list` — List all keys on the shared blackboard\n"
		teamSection += "\nUse the blackboard to share context, decisions, and intermediate results with your team."
	}
	return system + teamSection
}

// TeamResolver returns the team members for the calling agent from context.
// It is called at invocation time so that team changes are picked up dynamically.
type TeamResolver func(ctx context.Context) []string

// NewDelegateTool returns a tools.ToolDef for the delegate tool.
// sendFn is called when the tool is invoked to relay a message to another agent.
// teamResolver is called at invocation time to determine which agents the caller
// can delegate to; if it returns nil/empty, any agent name is accepted.
func NewDelegateTool(sendFn SendFunc, teamResolver TeamResolver) tools.ToolDef {
	return tools.ToolDef{
		Description: "Delegate a task to another agent on your team and get their response. Use this to assign work to team members.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			agent, _ := params["agent"].(string)
			message, _ := params["message"].(string)
			if agent == "" || message == "" {
				return "", fmt.Errorf("both agent and message are required")
			}
			// Resolve team dynamically from the calling process's agent definition.
			team := teamResolver(ctx)
			if len(team) > 0 {
				teamSet := make(map[string]bool, len(team))
				for _, t := range team {
					teamSet[t] = true
				}
				if !teamSet[agent] {
					return "", fmt.Errorf("agent %q is not on your team — you can only delegate to: %s",
						agent, strings.Join(team, ", "))
				}
			}
			return sendFn(ctx, agent, message)
		},
		Params: map[string]tools.ParamDef{
			"agent": {
				Type:        "string",
				Description: "Name of the team member agent to delegate to",
				Required:    true,
			},
			"message": {
				Type:        "string",
				Description: "The task or question to send to the agent",
				Required:    true,
			},
		},
	}
}

// RegisterDelegateTool registers the delegate tool on the given Tools instance
// if it is not already registered. teamResolver is called at invocation time to
// determine which agents the caller can delegate to.
// Returns true if registration happened.
func RegisterDelegateTool(t *tools.Tools, sendFn SendFunc, teamResolver TeamResolver) bool {
	for _, ts := range t.Schema() {
		if ts.Name == "delegate" {
			return false
		}
	}
	t.Register("delegate", NewDelegateTool(sendFn, teamResolver))
	return true
}

// DelegationContext holds extracted caller context for enriched delegation.
type DelegationContext struct {
	CallerAgent string
	Messages    []llm.Message
}

// ExtractCallerContext reads the last N messages from the caller process,
// optionally filtering by role. Returns nil if no messages match.
func ExtractCallerContext(callerProc *vega.Process, config *DelegationDef) *DelegationContext {
	if callerProc == nil || config == nil || config.ContextWindow <= 0 {
		return nil
	}

	msgs := callerProc.Messages()
	if len(msgs) == 0 {
		return nil
	}

	// Filter by role if specified
	if len(config.IncludeRoles) > 0 {
		roleSet := make(map[string]bool, len(config.IncludeRoles))
		for _, r := range config.IncludeRoles {
			roleSet[r] = true
		}
		filtered := make([]llm.Message, 0, len(msgs))
		for _, m := range msgs {
			if roleSet[string(m.Role)] {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	// Take last N messages
	if len(msgs) > config.ContextWindow {
		msgs = msgs[len(msgs)-config.ContextWindow:]
	}

	if len(msgs) == 0 {
		return nil
	}

	agentName := ""
	if callerProc.Agent != nil {
		agentName = callerProc.Agent.Name
	}

	return &DelegationContext{
		CallerAgent: agentName,
		Messages:    msgs,
	}
}

// FormatDelegationContext wraps the original message with caller context as XML.
func FormatDelegationContext(dc *DelegationContext, message string) string {
	if dc == nil || len(dc.Messages) == 0 {
		return message
	}

	var b strings.Builder
	b.WriteString("<delegation_context>\n<from>")
	b.WriteString(dc.CallerAgent)
	b.WriteString("</from>\n<recent_conversation>\n")
	for _, m := range dc.Messages {
		b.WriteString("[")
		b.WriteString(string(m.Role))
		b.WriteString("]: ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	b.WriteString("</recent_conversation>\n</delegation_context>\n\n<task>\n")
	b.WriteString(message)
	b.WriteString("\n</task>")
	return b.String()
}
