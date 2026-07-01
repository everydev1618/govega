package reactive

import (
	"fmt"
	"sync"
	"time"

	"github.com/everydev1618/govega/events"
)

// LoopGuardConfig configures the reactive safety backstop (§5.3). A zero or
// negative value disables that dimension.
type LoopGuardConfig struct {
	MaxDepth      int           // reactive-chain depth cap (0 = unlimited)
	RatePerWindow int           // max wakes per agent per RateWindow (0 = unlimited)
	RateWindow    time.Duration // sliding window for the rate limit
	DedupWindow   time.Duration // drop identical payloads seen within this span (0 = off)
	Now           func() time.Time
}

// LoopGuard enforces, independently of the salience gate, the limits that keep
// a reactive fleet from running away: chain depth, per-agent rate, and dedup of
// identical events. It is safe for concurrent use.
type LoopGuard struct {
	cfg LoopGuardConfig
	now func() time.Time

	mu     sync.Mutex
	hits   map[string][]time.Time // agent -> recent wake times (rate)
	lastBy map[string]time.Time   // agent+payload -> last seen (dedup)
}

// NewLoopGuard builds a guard from cfg. Now defaults to time.Now.
func NewLoopGuard(cfg LoopGuardConfig) *LoopGuard {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &LoopGuard{
		cfg:    cfg,
		now:    now,
		hits:   make(map[string][]time.Time),
		lastBy: make(map[string]time.Time),
	}
}

// Allow reports whether a wake for the given agent and event should proceed.
// On rejection it returns a short reason ("depth", "dedup", or "rate") suitable
// for the audit log. Checks run cheapest-first; depth needs no lock.
func (g *LoopGuard) Allow(agentName string, e events.Event) (bool, string) {
	// Depth: an event caused by a reactive chain that is already too deep.
	if g.cfg.MaxDepth > 0 && e.Origin != nil && e.Origin.Depth >= g.cfg.MaxDepth {
		return false, "depth"
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()

	// Dedup: identical (agent, type, payload) within the window.
	if g.cfg.DedupWindow > 0 {
		key := agentName + "\x00" + payloadKey(e)
		if last, ok := g.lastBy[key]; ok && now.Sub(last) < g.cfg.DedupWindow {
			return false, "dedup"
		}
		g.lastBy[key] = now
	}

	// Rate: max N wakes per agent per sliding window.
	if g.cfg.RatePerWindow > 0 && g.cfg.RateWindow > 0 {
		cutoff := now.Add(-g.cfg.RateWindow)
		recent := g.hits[agentName][:0:0]
		for _, t := range g.hits[agentName] {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) >= g.cfg.RatePerWindow {
			g.hits[agentName] = recent
			return false, "rate"
		}
		g.hits[agentName] = append(recent, now)
	}

	return true, ""
}

// payloadKey is a stable identity for an event's substance, used for dedup.
func payloadKey(e events.Event) string {
	return e.Type + "|" + fmt.Sprintf("%v", e.Data)
}
