package dsl

import (
	"context"
	"fmt"

	vega "github.com/everydev1618/govega"
)

// SendFunc sends a message to a named agent and returns the response.
type SendFunc func(ctx context.Context, agent string, message string) (string, error)

// BuildTeamPrompt appends team delegation instructions to a system prompt.
// agentDescriptions is optional — if a member has a description it is shown.
func BuildTeamPrompt(system string, team []string, agentDescriptions map[string]string) string {
	if len(team) == 0 {
		return system
	}
	teamSection := "\n\n## Your Team\n\nYou lead a team of agents. Use the `delegate` tool to assign tasks to them and get their responses. Your team members:\n"
	for _, name := range team {
		if desc, ok := agentDescriptions[name]; ok && desc != "" {
			teamSection += fmt.Sprintf("- **%s** — %s\n", name, desc)
		} else {
			teamSection += fmt.Sprintf("- **%s**\n", name)
		}
	}
	teamSection += "\nDelegate strategically — break complex tasks into pieces and assign them to the right team member. You can delegate multiple times, iterate on their work, and synthesize their outputs into a final result."
	return system + teamSection
}

// NewDelegateTool returns a vega.ToolDef for the delegate tool.
// sendFn is called when the tool is invoked to relay a message to another agent.
func NewDelegateTool(sendFn SendFunc) vega.ToolDef {
	return vega.ToolDef{
		Description: "Delegate a task to another agent on your team and get their response. Use this to assign work to team members.",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			agent, _ := params["agent"].(string)
			message, _ := params["message"].(string)
			if agent == "" || message == "" {
				return "", fmt.Errorf("both agent and message are required")
			}
			return sendFn(ctx, agent, message)
		},
		Params: map[string]vega.ParamDef{
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
// if it is not already registered. Returns true if registration happened.
func RegisterDelegateTool(tools *vega.Tools, sendFn SendFunc) bool {
	for _, ts := range tools.Schema() {
		if ts.Name == "delegate" {
			return false
		}
	}
	tools.Register("delegate", NewDelegateTool(sendFn))
	return true
}
