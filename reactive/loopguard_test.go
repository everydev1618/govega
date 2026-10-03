package reactive

import (
	"testing"
	"time"

	"github.com/everydev1618/govega/events"
)

// clock is a controllable time source for deterministic guard tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLoopGuardDepthCap(t *testing.T) {
	g := NewLoopGuard(LoopGuardConfig{MaxDepth: 3})

	shallow := events.Event{Type: "agent.completed", Origin: &events.Origin{Depth: 2}}
	if ok, _ := g.Allow("watcher", shallow); !ok {
		t.Fatal("depth 2 under cap 3 should be allowed")
	}
	deep := events.Event{Type: "agent.completed", Origin: &events.Origin{Depth: 3}}
	if ok, reason := g.Allow("watcher", deep); ok || reason != "depth" {
		t.Fatalf("depth 3 at cap 3 should be blocked with reason=depth, got ok=%v reason=%q", ok, reason)
	}
	// No Origin => root event, never depth-blocked.
	root := events.Event{Type: "agent.completed"}
	if ok, _ := g.Allow("watcher", root); !ok {
		t.Fatal("root event (nil Origin) must not be depth-blocked")
	}
}

func TestLoopGuardDedup(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	g := NewLoopGuard(LoopGuardConfig{DedupWindow: 10 * time.Second, Now: c.now})

	e := events.Event{Type: "agent.completed", Data: map[string]any{"agent": "builder", "status": "failed"}}

	if ok, _ := g.Allow("watcher", e); !ok {
		t.Fatal("first occurrence should pass")
	}
	if ok, reason := g.Allow("watcher", e); ok || reason != "dedup" {
		t.Fatalf("identical payload within window should be deduped, got ok=%v reason=%q", ok, reason)
	}
	// A different payload is not a duplicate.
	other := events.Event{Type: "agent.completed", Data: map[string]any{"agent": "builder", "status": "ok"}}
	if ok, _ := g.Allow("watcher", other); !ok {
		t.Fatal("distinct payload should pass despite dedup window")
	}
	// After the window elapses, the same payload passes again.
	c.advance(11 * time.Second)
	if ok, _ := g.Allow("watcher", e); !ok {
		t.Fatal("same payload after dedup window should pass again")
	}
}

func TestLoopGuardRate(t *testing.T) {
	c := &clock{t: time.Unix(2000, 0)}
	// Allow 3 per 60s window. Disable dedup so identical events aren't caught by it.
	g := NewLoopGuard(LoopGuardConfig{RatePerWindow: 3, RateWindow: 60 * time.Second, Now: c.now})

	mk := func(i int) events.Event {
		return events.Event{Type: "signal.custom", Data: map[string]any{"i": i}}
	}
	for i := range 3 {
		if ok, reason := g.Allow("worker", mk(i)); !ok {
			t.Fatalf("wake %d within rate should pass, got reason=%q", i, reason)
		}
	}
	if ok, reason := g.Allow("worker", mk(99)); ok || reason != "rate" {
		t.Fatalf("4th wake in window should be rate-limited, got ok=%v reason=%q", ok, reason)
	}
	// Slide past the window: budget refreshes.
	c.advance(61 * time.Second)
	if ok, reason := g.Allow("worker", mk(100)); !ok {
		t.Fatalf("wake after window should pass, got reason=%q", reason)
	}
}

// Rate is tracked per agent: one agent hitting its cap must not throttle another.
func TestLoopGuardRateIsPerAgent(t *testing.T) {
	c := &clock{t: time.Unix(3000, 0)}
	g := NewLoopGuard(LoopGuardConfig{RatePerWindow: 1, RateWindow: 60 * time.Second, Now: c.now})

	e := events.Event{Type: "x", Data: map[string]any{"n": 1}}
	if ok, _ := g.Allow("a", e); !ok {
		t.Fatal("agent a first wake should pass")
	}
	if ok, _ := g.Allow("b", e); !ok {
		t.Fatal("agent b must have its own budget, first wake should pass")
	}
	if ok, reason := g.Allow("a", events.Event{Type: "x", Data: map[string]any{"n": 2}}); ok || reason != "rate" {
		t.Fatalf("agent a second wake should be rate-limited, got ok=%v reason=%q", ok, reason)
	}
}
