package serve

import (
	"context"
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
	if result == "" {
		slog.Debug("reactive wake produced no output; nothing to consolidate",
			"agent", agentName, "event", e.Type)
		return
	}

	userID := reactiveOwnerUserID
	ts := time.Now()
	path := fmt.Sprintf("sessions/reactive-%s.md", ts.UTC().Format("2006-01-02-150405"))
	summary := fmt.Sprintf("Reacted to `%s`.\n\nEvent data: %v\n\nWhat I did:\n%s\n",
		e.Type, e.Data, result)
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
}
