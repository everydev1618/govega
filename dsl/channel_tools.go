package dsl

import (
	"context"
	"fmt"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// ChannelInfo holds minimal channel data returned to dsl tools.
type ChannelInfo struct {
	ID   string
	Name string
	Team []string
}

// ChannelBackend is the interface for channel operations.
// Defined here so dsl/ does not import serve/.
type ChannelBackend interface {
	CreateChannel(id, name, description, createdBy string, team []string) error
	GetChannelByName(name string) (*ChannelInfo, error)
	ListChannelsForAgent(agent string) ([]ChannelInfo, error)
	FindChannelForAgents(agent1, agent2 string) (channelID string, channelName string, err error)
	InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata string) (int64, error)
}

// ChannelPostCallback is called after an agent posts to a channel,
// so the server can publish SSE events to connected clients.
type ChannelPostCallback func(channelName, agent, content string, msgID int64)

// RegisterChannelTools registers channel tools on the interpreter.
func RegisterChannelTools(interp *Interpreter, backend ChannelBackend, onPost ChannelPostCallback) {
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
			description, _ := params["description"].(string)
			team := toStringSlice(params["team"])

			id := fmt.Sprintf("ch_%d", time.Now().UnixNano())
			if err := backend.CreateChannel(id, name, description, "mother", team); err != nil {
				return "", fmt.Errorf("create channel: %w", err)
			}
			return fmt.Sprintf("Channel **#%s** created with team: %v", name, team), nil
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
		},
	})
}
