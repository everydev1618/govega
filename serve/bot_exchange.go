package serve

import (
	"context"
	"log/slog"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
)

// botExchange is the shared inbound-message core for external chat bots
// (Discord, Telegram). It gives bot surfaces the same conversation
// continuity and memory behavior as the web chat handlers:
//
//   - the CallerResolver runs first, so claims-based identity is available
//     for memory scoping (apexvega#24)
//   - the process is rehydrated from persisted chat history when fresh
//     (server restart, failed-turn respawn) — without this the bot greets
//     the user as a stranger while the whole conversation sits in SQLite
//   - wiki memory is injected under the same userID namespace the web
//     surface reads and writes (see memoryUserID)
//   - both sides of the exchange are persisted to chat history
//   - onExchange fires after a successful turn; the serve layer wires it
//     to the memory curator so bot conversations feed long-term memory
type botExchange struct {
	interp    *dsl.Interpreter
	store     Store
	company   *dsl.Company
	surface   surface
	baseAgent string
	resolver  CallerResolver

	// onExchange receives the resolver-enriched ctx (identity, BYOK key)
	// plus the memory-scoped userID — NOT the platform user id.
	onExchange func(ctx context.Context, userID, agent, userMsg, response string)
}

// memoryUserID returns the memory namespace owner for a bot exchange:
// resolver-attached claims when present, else "default" — the same
// fallback the web surface uses (see chatUserID). Platform user ids
// (Telegram numeric id, Discord snowflake) are deliberately NOT used:
// vega is single-user-per-bot, and scoping memory by platform id gives
// each surface a disjoint namespace so cross-surface recall silently
// finds nothing.
func memoryUserID(ctx context.Context) string {
	if c, ok := ClaimsFrom(ctx); ok && c.UserID != "" {
		return c.UserID
	}
	return "default"
}

// run executes one bot turn against `agent` (the routed agent, which may
// differ from baseAgent via the "!agent" prefix). platformUserID is the
// surface-native user id; it is passed to the resolver only.
//
// onProgress, when non-nil, receives short interim status lines as the turn
// runs (e.g. "→ handing this to sage…" when the orchestrator dispatches to a
// sub-agent) so the user gets visibility into the multi-agent work a linear
// chat window otherwise hides. nil disables progress (silent turn).
func (b *botExchange) run(ctx context.Context, agent, text, platformUserID string, onProgress func(string), images []llm.ContentBlock) (string, error) {
	// Resolver first: it may attach claims that determine the memory
	// namespace for everything below.
	ctx = applyResolver(ctx, b.resolver, platformUserID)
	memUser := memoryUserID(ctx)

	// Visibility commands (/agents, /channels, /status) short-circuit the agent
	// turn: they answer instantly from the roster/channel state and are not
	// persisted to chat history (they're operator queries, not conversation).
	if cmd := botCommand(text); cmd != "" {
		return b.formatCommand(cmd, memUser), nil
	}

	// Load and inject memory into the process before sending.
	proc, err := b.interp.EnsureAgent(agent)
	if err == nil && proc != nil {
		hydrateProcess(b.store, proc, agent)
		memText := formatWikiMemoryForInjection(b.store, memUser, b.baseAgent)
		companyCtx := buildCompanyContext(b.company)
		if extra := buildExtraSystem(surfaceContext(b.surface), memText, "", companyCtx); extra != "" {
			proc.SetExtraSystem(extra)
		}
	}

	// Persist the user message. When images are attached, store a text
	// placeholder (we don't persist image bytes — pass-through for the turn),
	// so the dashboard shows the turn cleanly.
	persisted := text
	if len(images) > 0 {
		persisted = imagePlaceholder(len(images)) + text
	}
	if err := b.store.InsertChatMessage(agent, "user", persisted, nil); err != nil {
		slog.Warn("bot: failed to insert user message", "surface", b.surface, "error", err)
	}

	// Add memory context so tools can access the store.
	ctx = ContextWithMemory(ctx, b.store, memUser, b.baseAgent)

	// Stream the turn so we can surface dispatch/progress to the user. The
	// setup above (hydration, memory, persistence) is unchanged; only the send
	// is now streaming. We consume events for progress and take the final text
	// from the stream's accumulated response. Images ride along as vision
	// content blocks the agent reads this turn.
	stream, err := b.interp.StreamToAgentWithImages(ctx, agent, text, images)
	if err != nil {
		return "", err
	}
	seen := make(map[string]bool)
	for ev := range stream.Events() {
		if onProgress == nil {
			continue
		}
		if ev.Type == vega.ChatEventToolStart {
			if msg := dispatchProgress(ev.ToolName, ev.Arguments); msg != "" && !seen[msg] {
				seen[msg] = true
				onProgress(msg)
			}
		}
	}
	resp := stream.Response()
	if err := stream.Err(); err != nil {
		return "", err
	}

	// Persist assistant response.
	if err := b.store.InsertChatMessage(agent, "assistant", resp, nil); err != nil {
		slog.Warn("bot: failed to insert assistant message", "surface", b.surface, "error", err)
	}

	if b.onExchange != nil {
		b.onExchange(ctx, memUser, agent, text, resp)
	}
	return resp, nil
}
