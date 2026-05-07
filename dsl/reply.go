package dsl

import "context"

// ReplyTarget is the channel-of-origin abstraction. Entrypoints (Telegram
// bot, web chat handler, future SMS/Slack/etc. adapters) construct one
// and the serve layer registers it keyed by the agent name they're routing
// messages to (e.g. "apex" for the web app, "apex:123456789" for a
// per-user Telegram clone).
//
// When async work dispatched from that agent completes, the dispatch-
// complete callback uses the registered target to push the orchestrator's
// response back to the originating channel — Telegram users get a bot
// message on their chat, web users see an SSE-driven chat refresh.
//
// A nil ReplyTarget means "the inbox + chat history are the only sinks";
// the work still completes, but no proactive push happens.
type ReplyTarget interface {
	Reply(ctx context.Context, content string) error
}
