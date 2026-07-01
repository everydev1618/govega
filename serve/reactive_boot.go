package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/reactive"
	"github.com/everydev1618/govega/tools"
)

// reactiveOwnerUserID is the memory scope for reactive wakes. Vega is
// one-bot-one-user, so reactive cognition operates as the single owner.
const reactiveOwnerUserID = "default"

// Loop-guard defaults for reactive wakes. Depth caps reactive chains once
// causation is threaded (Phase 2); until then rate + dedup are the live
// backstops against an agent reacting to its own completions in a tight loop.
const (
	reactiveMaxDepth      = 4
	reactiveRatePerWindow = 12
	reactiveRateWindow    = time.Minute
	reactiveDedupWindow   = 5 * time.Second
)

// startReactive stands up the trigger router over the event spine and wires the
// emit_event tool. Cheap when no agent declares triggers: the router still
// drains the bus but ReactiveTriggers returns nothing, so no wakes fire.
func (s *Server) startReactive(ctx context.Context) {
	// Let interpreter-hosted tools (e.g. remember -> memory.wrote) emit onto
	// the spine.
	s.interp.SetEventPublisher(func(e events.Event) { s.bus.Publish(e) })

	// Distill reactive wakes into concise, outcome-aware memory notes.
	s.reactiveDistiller = s.distillReactiveWake

	s.registerEmitEventTool()

	s.reactiveRouter = reactive.NewRouter(reactive.Config{
		Bus:      s.bus,
		Dispatch: s.interp, // *dsl.Interpreter satisfies reactive.Dispatcher
		Triggers: s.interp, // ...and reactive.TriggerRegistry
		Guard: reactive.NewLoopGuard(reactive.LoopGuardConfig{
			MaxDepth:      reactiveMaxDepth,
			RatePerWindow: reactiveRatePerWindow,
			RateWindow:    reactiveRateWindow,
			DedupWindow:   reactiveDedupWindow,
		}),
		// Tier-2 salience gate for triggers that opt in with `gate: model`.
		Classifier: &llmClassifier{llm: s.getExtractLLM},
		// Attach the agent's own memory to the wake context, exactly as the
		// chat handlers do — this is what makes the reaction "rely on my
		// memory" (§5). Read-side continuity of self.
		Prepare: func(ctx context.Context, agentName string) context.Context {
			return ContextWithMemory(ctx, s.store, reactiveOwnerUserID, agentName)
		},
		// Write-side: consolidate the wake into memory when it ends, so the
		// agent accumulates experience across wakes (self-learning, §3.4).
		AfterWake: s.consolidateReactiveWake,
		// Audit every decision — wakes and gated-out events alike. Silent
		// suppression is a bug (§7).
		Audit: func(r reactive.Record) {
			verdict := "gated:" + r.Reason
			if r.Fired {
				verdict = "fired"
			}
			s.store.InsertEvent(StoreEvent{
				Type:      "reactive." + verdict,
				AgentName: r.Agent,
				Timestamp: time.Now(),
				Result:    r.Type,
			})
		},
	})
	s.reactiveRouter.Start(ctx)

	// Persist the spine to a durable log so the event history survives restart
	// and is queryable for audit. Note: we deliberately do NOT replay this log
	// into cognition on boot — re-firing yesterday's events would re-trigger
	// actions. Durability here means durability of record, not re-execution.
	go s.persistSpineEvents(ctx)
}

// persistSpineEvents writes every spine event to the durable events log.
func (s *Server) persistSpineEvents(ctx context.Context) {
	sub := s.bus.Subscribe(events.Durable, nil)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			agent, _ := e.Data["agent"].(string)
			data, _ := json.Marshal(e.Data)
			s.store.InsertEvent(StoreEvent{
				Type:      e.Type,
				AgentName: agent,
				Timestamp: e.Time,
				Data:      string(data),
			})
		}
	}
}

// registerEmitEventTool lets an agent publish a custom signal onto the spine,
// so agents can trigger each other explicitly (signal.custom).
func (s *Server) registerEmitEventTool() {
	s.interp.Tools().Register("emit_event", tools.ToolDef{
		Description: "Emit a custom event onto the reactive event bus so other agents subscribed to it can wake and react. Use for deliberate hand-offs and signals, not routine chatter.",
		Params: map[string]tools.ParamDef{
			"name": {
				Type:        "string",
				Description: "Short event name; published as signal.<name> (e.g. \"deploy_ready\" -> signal.deploy_ready).",
				Required:    true,
			},
			"detail": {
				Type:        "string",
				Description: "Optional human-readable detail describing what happened, passed to reacting agents.",
			},
		},
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			name, _ := params["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			detail, _ := params["detail"].(string)
			e := s.bus.Publish(events.Event{
				Type: "signal." + name,
				Data: map[string]any{"detail": detail},
			})
			return fmt.Sprintf("emitted %s (%s)", e.Type, e.ID), nil
		}),
	})
}

// consolidateReactiveWake writes an outcome-aware session note after a reactive
// wake, regardless of conversation length — the fix for "reactive bursts are
// too short to hit the 40-message compaction threshold" (§3.3/§3.4). Rules-gated
// for importance: a wake that produced no output is a no-op and is dropped
// (logged, not silent). LLM-distilled summaries and an importance classifier
// are Phase 2 (§8 open decision #5).
func (s *Server) consolidateReactiveWake(ctx context.Context, agentName string, e events.Event, result string) {
	result = strings.TrimSpace(result)

	// Importance gate (§3.4): drop no-op reactions so the wiki doesn't fill with
	// noise. Logged, never silent.
	if reactiveIsNoOp(result) {
		slog.Debug("reactive wake was a no-op; nothing to consolidate",
			"agent", agentName, "event", e.Type)
		return
	}

	userID := reactiveOwnerUserID
	ts := time.Now()
	// Nanosecond precision so rapid successive wakes don't collide on one path
	// (which would overwrite prior notes and undercount for promotion).
	path := fmt.Sprintf("sessions/reactive-%s.md", ts.UTC().Format("2006-01-02-150405.000000000"))

	// Body: an LLM-distilled summary if a distiller is wired (production),
	// otherwise a legible templated note (tests / no-LLM). Either way the note
	// is outcome-aware: what fired, and what the agent did about it.
	body := fmt.Sprintf("What I did:\n%s\n", result)
	if s.reactiveDistiller != nil {
		if d := strings.TrimSpace(s.reactiveDistiller(ctx, e, result)); d != "" {
			body = d + "\n"
		}
	}
	summary := fmt.Sprintf("Reacted to `%s`.\n\nEvent data: %v\n\n%s", e.Type, e.Data, body)
	frontmatter := fmt.Sprintf("active: true\nsource: reactive-wake\nagent: %s\nevent: %s\nat: %s",
		agentName, e.Type, ts.UTC().Format(time.RFC3339))

	// Demote older active notes first so injection stays bounded, matching the
	// length-based compaction path.
	if err := s.demotePriorActiveSessions(userID, agentName); err != nil {
		slog.Warn("reactive consolidation: demote prior sessions failed", "agent", agentName, "error", err)
	}
	if err := s.store.UpsertMemoryPage(MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: agentName, UserID: userID,
		Path: path, Content: summary, Frontmatter: frontmatter,
	}); err != nil {
		slog.Warn("reactive consolidation: write session note failed", "agent", agentName, "error", err)
		return
	}
	if err := s.store.ReplaceMemoryLinks(MemoryScopeAgent, agentName, userID, path, extractMemoryLinks(summary)); err != nil {
		slog.Warn("reactive consolidation: link session note failed", "agent", agentName, "error", err)
	}
	if err := s.linkSessionInIndex(userID, agentName, path, ts); err != nil {
		slog.Warn("reactive consolidation: index session note failed", "agent", agentName, "error", err)
	}

	// Promotion (§3.6): once the agent has reacted to this event type enough
	// times, promote a durable line into MEMORY.md — the always-injected
	// "earned character" band (D4). Repeated experience becomes disposition.
	s.maybePromoteRecurringReaction(userID, agentName, e.Type)
}

// reactiveNoOpPhrases are short results that indicate the wake decided there
// was nothing to do. Kept conservative so real (if terse) work is still kept.
var reactiveNoOpPhrases = []string{
	"nothing to do", "no action needed", "no action required",
	"nothing to report", "no-op", "ignored", "not relevant",
}

func reactiveIsNoOp(result string) bool {
	if result == "" {
		return true
	}
	low := strings.ToLower(result)
	// Only treat as no-op when the whole (short) result reads as a dismissal.
	if len(result) <= 80 {
		for _, p := range reactiveNoOpPhrases {
			if strings.Contains(low, p) {
				return true
			}
		}
	}
	return false
}

// reactivePromotionThreshold is how many times an agent must react to the same
// event type before the pattern is promoted into MEMORY.md.
const reactivePromotionThreshold = 3

// maybePromoteRecurringReaction counts how often the agent has reacted to
// eventType and, past the threshold, records a durable line in MEMORY.md so the
// recurring pattern is always in the agent's head. Best-effort.
func (s *Server) maybePromoteRecurringReaction(userID, agentName, eventType string) {
	pages, err := s.store.ListMemoryPages(MemoryScopeAgent, agentName, userID, "sessions/reactive-")
	if err != nil {
		return
	}
	count := 0
	needle := "event: " + eventType + "\n"
	for _, p := range pages {
		// Match on the frontmatter's event line; tolerate it being the last line.
		if strings.Contains(p.Frontmatter+"\n", needle) {
			count++
		}
	}
	if count < reactivePromotionThreshold {
		return
	}

	line := fmt.Sprintf("- Recurring: I regularly react to `%s` (seen %d times).", eventType, count)
	const path = "MEMORY.md"
	existing, _ := s.store.GetMemoryPage(MemoryScopeAgent, agentName, userID, path)
	var content string
	if existing != nil {
		content = existing.Content
	}
	marker := fmt.Sprintf("react to `%s`", eventType)
	if strings.Contains(content, marker) {
		// Update the count in place by replacing the whole prior line.
		lines := strings.Split(content, "\n")
		for i, l := range lines {
			if strings.Contains(l, marker) {
				lines[i] = line
				break
			}
		}
		content = strings.Join(lines, "\n")
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += line + "\n"
	}
	if err := s.store.UpsertMemoryPage(MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: agentName, UserID: userID,
		Path: path, Content: content, Frontmatter: existingFrontmatter(existing),
	}); err != nil {
		slog.Warn("reactive promotion: update MEMORY.md failed", "agent", agentName, "error", err)
	}
}

func existingFrontmatter(p *MemoryPage) string {
	if p != nil {
		return p.Frontmatter
	}
	return ""
}
