package serve

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/everydev1618/govega/dsl"
)

var (
	reactiveCooldownMu sync.Mutex
	reactiveCooldowns  = make(map[string]time.Time)
)

// canNotify returns true if the agent has not been notified for the given
// channel within the cooldown period (30s). This prevents flooding agents
// with repeated notifications in a short time window.
func canNotify(channelName, agent string) bool {
	key := channelName + ":" + agent
	reactiveCooldownMu.Lock()
	defer reactiveCooldownMu.Unlock()
	if last, ok := reactiveCooldowns[key]; ok && time.Since(last) < 30*time.Second {
		return false
	}
	reactiveCooldowns[key] = time.Now()
	return true
}

// notifyChannelTeammate sends a channel message notification to a teammate
// so they can decide whether to respond. The depth parameter is used to
// track reactive depth and prevent infinite loops. triggerMsgID is the
// message to thread replies under.
func (s *Server) notifyChannelTeammate(channelName, targetAgent, poster, message string, depth int, social bool, triggerMsgID int64) {
	if !canNotify(channelName, targetAgent) {
		return
	}

	proc, err := s.interp.EnsureAgent(targetAgent)
	if err != nil {
		slog.Warn("channel reactive: failed to ensure agent", "agent", targetAgent, "error", err)
		return
	}

	// Inject memory so the agent has context.
	s.hydrateAgent(proc, targetAgent)

	preview := message
	if len(preview) > 300 {
		preview = preview[:300] + "..."
	}

	var prompt string
	if social {
		prompt = fmt.Sprintf(
			`[Channel #%s] %s said (message %d):
"%s"

This is the social/fun channel! Jump in with your own personality — share a thought, joke, reaction, or hot take. Be yourself, keep it short (1-2 sentences), and use post_to_channel(channel="%s", message="...", thread_id=%d) to respond in the thread. Don't be formal.`,
			channelName, poster, triggerMsgID, preview, channelName, triggerMsgID,
		)
	} else {
		prompt = fmt.Sprintf(
			`[Channel #%s] %s posted (message %d):
"%s"

If this is relevant to your work or needs your input, respond using post_to_channel(channel="%s", message="...", thread_id=%d) to reply in the thread. If not, just say "noted" and move on. Keep responses brief.`,
			channelName, poster, triggerMsgID, preview, channelName, triggerMsgID,
		)
	}

	ctx := dsl.ContextWithChannelReactiveDepth(context.Background(), depth+1)
	ctx = ContextWithDomainStore(ctx, s.sqliteStore)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	resp, err := s.interp.SendToAgent(ctx, targetAgent, prompt)
	if err != nil {
		slog.Warn("channel reactive: failed to send to agent", "agent", targetAgent, "channel", channelName, "error", err)
		return
	}
	_ = resp
}

// notifyChannelTeammates notifies all channel team members (except the poster)
// about a new message. Used to get multiple agents to respond to user messages.
// triggerMsgID is the message to thread replies under.
// Notifications are staggered by 2 seconds to avoid overwhelming the LLM API.
func (s *Server) notifyChannelTeammates(ch *Channel, poster, message string, depth int, triggerMsgID int64) {
	social := ch.Mode == "social"
	go func() {
		for i, member := range ch.Team {
			if member == poster {
				continue
			}
			if i > 0 {
				time.Sleep(2 * time.Second)
			}
			m := member
			go s.notifyChannelTeammate(ch.Name, m, poster, message, depth, social, triggerMsgID)
		}
	}()
}
