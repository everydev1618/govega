package serve

import (
	"github.com/everydev1618/govega/dsl"
)

// Reply-target registry. Maps agent name → the ReplyTarget that knows
// how to push a message back to the originating channel for that agent.
// Keys look like "apex" (web app), "apex:1992054241" (Telegram per-user
// clone), or any other agent with an active reverse-channel.
//
// Entries are added when a channel adapter (Telegram bot, web chat
// handler) processes an incoming message — they "claim" the agent for
// the duration of the conversation. Stale entries are harmless since
// Reply() failures are logged but not fatal.

// RegisterReplyTarget binds a ReplyTarget to an agent name. Subsequent
// async dispatch completions originating from this agent will push their
// response through the given target. Re-registering the same agent name
// replaces the previous target.
func (s *Server) RegisterReplyTarget(agentName string, target dsl.ReplyTarget) {
	if agentName == "" || target == nil {
		return
	}
	s.replyTargetsMu.Lock()
	if s.replyTargets == nil {
		s.replyTargets = make(map[string]dsl.ReplyTarget)
	}
	s.replyTargets[agentName] = target
	s.replyTargetsMu.Unlock()
}

// UnregisterReplyTarget removes a previously-registered target. Safe to
// call when no target is registered.
func (s *Server) UnregisterReplyTarget(agentName string) {
	s.replyTargetsMu.Lock()
	delete(s.replyTargets, agentName)
	s.replyTargetsMu.Unlock()
}

// lookupReplyTarget returns the target for agentName, or nil if none.
func (s *Server) lookupReplyTarget(agentName string) dsl.ReplyTarget {
	s.replyTargetsMu.RLock()
	defer s.replyTargetsMu.RUnlock()
	if s.replyTargets == nil {
		return nil
	}
	return s.replyTargets[agentName]
}
