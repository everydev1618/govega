package serve

import (
	"context"
	"fmt"
	"strings"

	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/llm"
)

// llmClassifier is the tier-2 salience gate (§5.2): a one-shot, cheap LLM call
// that decides whether an event genuinely warrants waking an agent, for
// triggers that opt in with `gate: model`. It fails OPEN — any error or missing
// backend lets the wake through, so a classifier problem never silently
// swallows reactions.
type llmClassifier struct {
	llm func() llm.LLM
}

func (c *llmClassifier) Salient(ctx context.Context, agentName, wakePrompt string, e events.Event) (bool, string) {
	backend := c.llm()
	if backend == nil {
		return true, "no-classifier"
	}

	sys := "You are a fast salience filter for a reactive AI agent. Given an event and the action the agent would take, decide whether the agent should actually wake and act right now, or whether this is noise it should ignore. Answer with exactly YES or NO as the first word, then a brief reason on the same line."
	user := fmt.Sprintf(
		"Agent: %s\nEvent type: %s\nEvent data: %v\nProposed action: %s\n\nShould the agent wake and act? Answer YES or NO.",
		agentName, e.Type, e.Data, wakePrompt,
	)

	resp, err := backend.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: sys},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil || resp == nil {
		return true, "classifier-error"
	}

	text := strings.TrimSpace(resp.Content)
	reason := text
	if i := strings.IndexAny(reason, "\n"); i >= 0 {
		reason = reason[:i]
	}
	if strings.HasPrefix(strings.ToUpper(text), "NO") {
		return false, reason
	}
	return true, reason
}

// distillReactiveWake turns a reactive wake's (event, result) into a concise,
// outcome-aware memory note via the extract LLM. Returns "" on any error so
// the caller falls back to the templated note (fail-safe, never blocks the
// consolidation).
func (s *Server) distillReactiveWake(ctx context.Context, e events.Event, result string) string {
	backend := s.getExtractLLM()
	if backend == nil {
		return ""
	}
	sys := "You distill an AI agent's reactive action into a terse memory note (1-3 sentences) it will read next time a similar event fires. Capture what happened and what the agent did about it and how it turned out. No preamble."
	user := fmt.Sprintf("Event type: %s\nEvent data: %v\n\nWhat the agent did:\n%s", e.Type, e.Data, result)
	resp, err := backend.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: sys},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil || resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Content)
}
