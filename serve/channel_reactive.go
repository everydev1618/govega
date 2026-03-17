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
// track reactive depth and prevent infinite loops.
func (s *Server) notifyChannelTeammate(channelName, targetAgent, poster, message string, depth int) {
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

	prompt := fmt.Sprintf(
		`[Channel #%s] %s posted:
"%s"

If this is relevant to your work or needs your input, respond using post_to_channel(channel="%s", message="..."). If not, just say "noted" and move on. Keep responses brief.`,
		channelName, poster, preview, channelName,
	)

	ctx := dsl.ContextWithChannelReactiveDepth(context.Background(), depth+1)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	resp, err := s.interp.SendToAgent(ctx, targetAgent, prompt)
	if err != nil {
		slog.Warn("channel reactive: failed to send to agent", "agent", targetAgent, "channel", channelName, "error", err)
		return
	}
	_ = resp
}
