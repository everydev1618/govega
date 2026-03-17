package dsl

import (
	"context"
	"fmt"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// contextKey is a private type for context keys in this package.
type contextKey string

const channelReactiveDepthKey contextKey = "channelReactiveDepth"

// ChannelReactiveDepthFromContext returns the current reactive depth from ctx.
func ChannelReactiveDepthFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(channelReactiveDepthKey).(int); ok {
		return v
	}
	return 0
}

// ContextWithChannelReactiveDepth returns a new context with the given reactive depth.
func ContextWithChannelReactiveDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, channelReactiveDepthKey, depth)
}

// MaxReactiveDepth is the maximum depth for reactive channel notifications
// to prevent infinite loops.
const MaxReactiveDepth = 2

// ChannelReactiveCallback is called after a post_to_channel so that other
// team members can be notified and optionally respond.
type ChannelReactiveCallback func(channelName string, team []string, poster string, message string, depth int)

// ChannelInfo holds minimal channel data returned to dsl tools.
type ChannelInfo struct {
	ID   string
	Name string
	Team []string
}

// ChannelMessage holds a single message returned by RecentChannelMessages.
type ChannelMessage struct {
	Agent   string
	Content string
}

// ChannelBackend is the interface for channel operations.
// Defined here so dsl/ does not import serve/.
type ChannelBackend interface {
	CreateChannel(id, name, description, createdBy string, team []string, mode string) error
	GetChannelByName(name string) (*ChannelInfo, error)
	ListChannelsForAgent(agent string) ([]ChannelInfo, error)
	FindChannelForAgents(agent1, agent2 string) (channelID string, channelName string, err error)
	InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata string) (int64, error)
	RecentChannelMessages(channelID string, limit int) ([]ChannelMessage, error)
}

// ChannelPostCallback is called after an agent posts to a channel,
// so the server can publish SSE events to connected clients.
type ChannelPostCallback func(channelName, agent, content string, msgID int64)

// RegisterChannelTools registers channel tools on the interpreter.
func RegisterChannelTools(interp *Interpreter, backend ChannelBackend, onPost ChannelPostCallback, onReactive ChannelReactiveCallback) {
	t := interp.Tools()

	t.Register("post_to_channel", tools.ToolDef{
		Description: "Post a message to a team channel. Use this to share updates, progress, questions, and decisions with your team. The user can see all channel messages in the UI.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			channelName, _ := params["channel"].(string)
			if channelName == "" {
				return "", fmt.Errorf("channel is required")
			}
			message, _ := params["message"].(string)
			if message == "" {
				return "", fmt.Errorf("message is required")
			}

			// Resolve calling agent name from context.
			agent := "unknown"
			if proc := vega.ProcessFromContext(ctx); proc != nil && proc.Agent != nil {
				agent = proc.Agent.Name
			}

			ch, err := backend.GetChannelByName(channelName)
			if err != nil || ch == nil {
				return "", fmt.Errorf("channel #%s not found", channelName)
			}

			msgID, err := backend.InsertChannelMessage(ch.ID, agent, "assistant", message, nil, "")
			if err != nil {
				return "", fmt.Errorf("post to channel: %w", err)
			}

			if onPost != nil {
				onPost(channelName, agent, message, msgID)
			}

			if onReactive != nil {
				depth := ChannelReactiveDepthFromContext(ctx)
				if depth < MaxReactiveDepth {
					onReactive(channelName, ch.Team, agent, message, depth)
				}
			}

			return fmt.Sprintf("Posted to #%s (message %d)", channelName, msgID), nil
		}),
		Params: map[string]tools.ParamDef{
			"channel": {
				Type:        "string",
				Description: "Channel name to post to (e.g. 'design-team', 'content-squad')",
				Required:    true,
			},
			"message": {
				Type:        "string",
				Description: "The message to post",
				Required:    true,
			},
		},
	})

	t.Register("list_my_channels", tools.ToolDef{
		Description: "List the channels you belong to. Returns channel names and team members.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			agent := "unknown"
			if proc := vega.ProcessFromContext(ctx); proc != nil && proc.Agent != nil {
				agent = proc.Agent.Name
			}

			channels, err := backend.ListChannelsForAgent(agent)
			if err != nil {
				return "", fmt.Errorf("list channels: %w", err)
			}
			if len(channels) == 0 {
				return "You are not in any channels.", nil
			}

			var result string
			for _, ch := range channels {
				result += fmt.Sprintf("- #%s (team: %v)\n", ch.Name, ch.Team)
			}
			return result, nil
		}),
	})

	t.Register("create_channel", tools.ToolDef{
		Description: "Create a Slack-style channel for a team of agents to collaborate in. The user can see and participate in the channel from the UI.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			// Strip leading # if provided — the UI adds the # prefix.
			name = strings.TrimPrefix(name, "#")
			description, _ := params["description"].(string)
			mode, _ := params["mode"].(string)
			team := toStringSlice(params["team"])

			// For company-wide channels, auto-populate team with all agents if empty.
			if len(team) == 0 && (name == "general" || name == "random") {
				interp.mu.RLock()
				for n := range interp.Document().Agents {
					if n != motherAgentName && n != hermesAgentName {
						team = append(team, n)
					}
				}
				interp.mu.RUnlock()
			}

			// Default: #random is social mode.
			if mode == "" && name == "random" {
				mode = "social"
			}

			if len(team) == 0 {
				return "", fmt.Errorf("team is required — provide at least one agent name")
			}

			id := fmt.Sprintf("ch_%d", time.Now().UnixNano())
			if err := backend.CreateChannel(id, name, description, "mother", team, mode); err != nil {
				return "", fmt.Errorf("create channel: %w", err)
			}
			modeMsg := ""
			if mode == "social" {
				modeMsg = " (social mode — all members respond to messages)"
			}
			return fmt.Sprintf("Channel **#%s** created with team: %v%s", name, team, modeMsg), nil
		}),
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Channel name (lowercase, no spaces — e.g. 'design-review', 'content-team')",
				Required:    true,
			},
			"description": {
				Type:        "string",
				Description: "Short description of the channel's purpose",
			},
			"team": {
				Type:        "array",
				Description: "List of agent names to include in the channel",
				Required:    true,
			},
			"mode": {
				Type:        "string",
				Description: "Channel mode: 'social' means ALL members respond to every message (great for watercooler/fun channels). Default is normal mode where only the team lead responds.",
			},
		},
	})
}
