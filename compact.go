package vega

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everydev1618/govega/llm"
)

// CompactionMeta describes a single compaction event. It travels with
// the distilled summary so the sink can tag and route the result —
// e.g. write a wiki note titled after the agent and timestamp.
type CompactionMeta struct {
	ProcessID    string
	AgentName    string
	DroppedCount int       // number of messages summarized and removed
	KeptCount    int       // messages remaining in the conversation after compaction
	DroppedAt    time.Time // wall-clock time the compaction ran
}

// CompactionSink receives the distilled summary of messages being
// dropped from a process's conversation. Implementations typically
// persist the summary so it can be recalled later — e.g. write it as
// a wiki page so subsequent turns surface it via memory injection.
// The sink runs synchronously: returning an error aborts the
// compaction (no messages are trimmed).
type CompactionSink func(ctx context.Context, summary string, meta CompactionMeta) error

// distillationPrompt is the system instruction handed to the LLM
// when compacting old messages. It biases the model toward keeping
// content the *user* will care about across future conversations —
// names, decisions, preferences, open threads, artifacts — and away
// from transient mechanics that won't matter later.
const distillationPrompt = `You are condensing the older part of a conversation so it can be remembered later.

Output a markdown summary optimized for *recall*, not brevity. The agent will see this summary again in future turns and the user must feel like the agent never forgot.

Keep:
- Things the user said about themselves (name, role, preferences, context)
- Decisions made and reasons given
- Open threads, unanswered questions, things to follow up on
- Artifacts the user shared (file paths, links, names, numbers)
- Anything the user explicitly said matters

Summarize aggressively:
- Tool calls, tool results, retries, debug output
- Routine acknowledgements and pleasantries

Drop:
- Failed attempts that were later corrected
- Reasoning that was wrong or superseded

Structure with short sections (## Facts about the user, ## Decisions, ## Open threads, ## Artifacts). Skip a section if it has no content.`

// Compact distills the oldest portion of the conversation into a
// single summary, hands it to sink for persistence, and trims those
// messages from p.messages — keeping the most recent keepLast turns
// verbatim. The trim boundary is walked forward to land on a user
// message so tool_use/tool_result pairs aren't split across the
// boundary (the API rejects mid-pair history).
//
// No-op when the conversation has fewer than keepLast+1 messages.
// Errors from the LLM call or from sink abort compaction without
// mutating p.messages.
func (p *Process) Compact(ctx context.Context, keepLast int, sink CompactionSink) error {
	if sink == nil {
		return errors.New("compact: sink is nil")
	}
	if p.llm == nil {
		return errors.New("compact: process has no LLM backend")
	}

	p.mu.RLock()
	total := len(p.messages)
	p.mu.RUnlock()
	if total <= keepLast {
		return nil
	}

	// Choose the split point: oldest (total-keepLast) get summarized,
	// the rest stay verbatim. Walk forward from the naive split to land
	// on the first user message so we don't orphan an assistant
	// tool_use from its tool_result.
	naive := total - keepLast
	p.mu.RLock()
	split := -1
	for i := naive; i < total; i++ {
		if p.messages[i].Role == llm.RoleUser {
			split = i
			break
		}
	}
	if split <= 0 {
		// No clean boundary — nothing safe to drop.
		p.mu.RUnlock()
		return nil
	}
	toSummarize := make([]llm.Message, split)
	copy(toSummarize, p.messages[:split])
	p.mu.RUnlock()

	summary, err := p.distill(ctx, toSummarize)
	if err != nil {
		return fmt.Errorf("compact: distill: %w", err)
	}

	meta := CompactionMeta{
		ProcessID:    p.ID,
		AgentName:    p.Agent.Name,
		DroppedCount: split,
		KeptCount:    total - split,
		DroppedAt:    time.Now(),
	}
	if err := sink(ctx, summary, meta); err != nil {
		return fmt.Errorf("compact: sink: %w", err)
	}

	// Trim only after the sink succeeded — if anything blew up, the
	// conversation still carries its full history.
	p.mu.Lock()
	// Recompute the trim in case messages shifted while we were calling
	// the LLM. We anchor on the same split point relative to the
	// original slice: drop the prefix of length `split`.
	if len(p.messages) >= split {
		trimmed := make([]llm.Message, len(p.messages)-split)
		copy(trimmed, p.messages[split:])
		p.messages = trimmed
	}
	p.mu.Unlock()

	return nil
}

// distill calls the LLM to produce the summary text. Uses Generate
// (non-streaming) — compaction is a one-shot ask and we want the
// complete output before trimming.
func (p *Process) distill(ctx context.Context, msgs []llm.Message) (string, error) {
	var b strings.Builder
	b.WriteString("Conversation excerpt to summarize:\n\n")
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}

	req := []llm.Message{
		{Role: llm.RoleSystem, Content: distillationPrompt},
		{Role: llm.RoleUser, Content: b.String()},
	}
	resp, err := p.llm.Generate(ctx, req, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", errors.New("distiller returned empty summary")
	}
	return resp.Content, nil
}
