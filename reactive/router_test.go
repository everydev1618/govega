package reactive

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everydev1618/govega/events"
)

type fakeRegistry struct{ m map[string][]Trigger }

func (f fakeRegistry) ReactiveTriggers() map[string][]Trigger { return f.m }

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []dispatchCall
	fired chan dispatchCall
}

type dispatchCall struct {
	agent string
	msg   string
}

func (d *fakeDispatcher) SendToAgent(_ context.Context, agent, msg string) (string, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dispatchCall{agent, msg})
	d.mu.Unlock()
	if d.fired != nil {
		d.fired <- dispatchCall{agent, msg}
	}
	return "", nil
}

func decisionFor(ds []decision, agent string) (decision, bool) {
	for _, d := range ds {
		if d.Agent == agent {
			return d, true
		}
	}
	return decision{}, false
}

func TestEvaluateFiresMatchingAgent(t *testing.T) {
	reg := fakeRegistry{m: map[string][]Trigger{
		"night-watch": {{
			On:     "agent.*",
			Where:  "status == failed",
			Prompt: "Agent {{.Data.agent}} failed: {{.Data.error}}.",
		}},
	}}
	r := NewRouter(Config{Triggers: reg})

	e := events.Event{Type: "agent.completed", Data: map[string]any{
		"agent":  "builder",
		"status": "failed",
		"error":  "boom",
	}}
	d, ok := decisionFor(r.evaluate(e), "night-watch")
	if !ok || !d.Fired {
		t.Fatalf("expected night-watch to fire, got %+v (ok=%v)", d, ok)
	}
	if !strings.Contains(d.Message, "builder") || !strings.Contains(d.Message, "boom") {
		t.Fatalf("prompt not interpolated from event data: %q", d.Message)
	}
}

func TestEvaluateSkipsNonMatching(t *testing.T) {
	reg := fakeRegistry{m: map[string][]Trigger{
		"night-watch": {{On: "agent.*", Where: "status == failed", Prompt: "x"}},
		"cron-only":   {{On: "schedule.*", Prompt: "y"}},
	}}
	r := NewRouter(Config{Triggers: reg})

	// A successful completion: type matches night-watch but predicate rejects;
	// cron-only's type does not match at all.
	e := events.Event{Type: "agent.completed", Data: map[string]any{"status": "ok"}}
	for _, d := range r.evaluate(e) {
		if d.Fired {
			t.Fatalf("no agent should fire for status=ok, but %s did", d.Agent)
		}
	}
}

func TestEvaluateGuardBlocks(t *testing.T) {
	reg := fakeRegistry{m: map[string][]Trigger{
		"watcher": {{On: "agent.*", Prompt: "react"}},
	}}
	r := NewRouter(Config{
		Triggers: reg,
		Guard:    NewLoopGuard(LoopGuardConfig{MaxDepth: 1}),
	})

	e := events.Event{Type: "agent.completed", Origin: &events.Origin{Depth: 1}}
	d, _ := decisionFor(r.evaluate(e), "watcher")
	if d.Fired {
		t.Fatal("guard should have blocked a depth-1 event at cap 1")
	}
	if d.Reason != "depth" {
		t.Fatalf("expected reason=depth, got %q", d.Reason)
	}
}

func TestEvaluateOneWakePerAgent(t *testing.T) {
	// Two triggers on the same agent both match the same event; the agent
	// should wake at most once (first match wins).
	reg := fakeRegistry{m: map[string][]Trigger{
		"watcher": {
			{On: "agent.*", Prompt: "first"},
			{On: "agent.completed", Prompt: "second"},
		},
	}}
	r := NewRouter(Config{Triggers: reg})

	ds := r.evaluate(events.Event{Type: "agent.completed"})
	fired := 0
	var msg string
	for _, d := range ds {
		if d.Agent == "watcher" && d.Fired {
			fired++
			msg = d.Message
		}
	}
	if fired != 1 {
		t.Fatalf("agent should wake exactly once, fired %d times", fired)
	}
	if msg != "first" {
		t.Fatalf("first matching trigger should win, got %q", msg)
	}
}

func TestEvaluateMalformedPredicateDoesNotFire(t *testing.T) {
	reg := fakeRegistry{m: map[string][]Trigger{
		"broken": {{On: "agent.*", Where: "no operator here", Prompt: "x"}},
	}}
	r := NewRouter(Config{Triggers: reg})

	d, _ := decisionFor(r.evaluate(events.Event{Type: "agent.completed"}), "broken")
	if d.Fired {
		t.Fatal("a malformed predicate must not fire the agent")
	}
	if d.Reason == "" {
		t.Fatal("expected a reason recording the predicate error")
	}
}

func TestStartDeliversWakeEndToEnd(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()

	disp := &fakeDispatcher{fired: make(chan dispatchCall, 1)}
	reg := fakeRegistry{m: map[string][]Trigger{
		"night-watch": {{On: "agent.*", Where: "status == failed", Prompt: "handle {{.Data.agent}}"}},
	}}
	r := NewRouter(Config{Bus: bus, Dispatch: disp, Triggers: reg})

	r.Start(t.Context())

	// An irrelevant event should not wake anyone.
	bus.Publish(events.Event{Type: "agent.completed", Data: map[string]any{"status": "ok"}})
	// The relevant one should.
	bus.Publish(events.Event{Type: "agent.completed", Data: map[string]any{"agent": "builder", "status": "failed"}})

	select {
	case c := <-disp.fired:
		if c.agent != "night-watch" || !strings.Contains(c.msg, "builder") {
			t.Fatalf("unexpected wake: %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("router never dispatched the reactive wake")
	}
}

func TestPrepareContextHookRuns(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()

	type ctxKey string
	const memKey ctxKey = "mem"

	disp := &fakeDispatcher{fired: make(chan dispatchCall, 1)}
	got := make(chan any, 1)
	reg := fakeRegistry{m: map[string][]Trigger{"a": {{On: "*", Prompt: "go"}}}}
	r := NewRouter(Config{
		Bus:      bus,
		Dispatch: disp,
		Triggers: reg,
		Prepare: func(ctx context.Context, agent string) context.Context {
			return context.WithValue(ctx, memKey, "attached:"+agent)
		},
	})
	// Wrap dispatch to capture context value via a custom dispatcher.
	r.cfg.Dispatch = dispatcherFunc(func(ctx context.Context, agent, msg string) (string, error) {
		got <- ctx.Value(memKey)
		return "", nil
	})

	r.Start(t.Context())
	bus.Publish(events.Event{Type: "signal.custom"})

	select {
	case v := <-got:
		if v != "attached:a" {
			t.Fatalf("Prepare hook not applied to dispatch context, got %v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch never ran")
	}
}

// A self-referential trigger (an agent that reacts to agent.completed and whose
// wake emits agent.completed) must terminate at the loop guard's MaxDepth,
// proving causation threading gives the depth guard teeth (Phase 2). Dedup and
// rate are disabled so depth is the sole limiter.
func TestReactiveChainTerminatesAtMaxDepth(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()

	var mu sync.Mutex
	depthsSeen := []int{}
	// The dispatcher stands in for cognition; it also lets us observe the
	// Origin depth threaded onto each wake's context.
	disp := dispatcherFunc(func(ctx context.Context, _, _ string) (string, error) {
		if o := events.OriginFromContext(ctx); o != nil {
			mu.Lock()
			depthsSeen = append(depthsSeen, o.Depth)
			mu.Unlock()
		}
		return "", nil
	})
	reg := fakeRegistry{m: map[string][]Trigger{"echo": {{On: "agent.*", Prompt: "again"}}}}
	r := NewRouter(Config{
		Bus:      bus,
		Dispatch: disp,
		Triggers: reg,
		Guard:    NewLoopGuard(LoopGuardConfig{MaxDepth: 3}),
	})
	r.Start(t.Context())

	// Kick off the chain with a root event (depth 0).
	bus.Publish(events.Event{Type: "agent.completed", Data: map[string]any{"status": "ok"}})

	// Chain: depth0 wake -> emits depth1 -> wake -> emits depth2 -> wake ->
	// emits depth3 -> guard blocks (3 >= MaxDepth). So exactly 3 wakes, at
	// context depths 1, 2, 3.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(depthsSeen)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("chain did not reach 3 wakes; saw %d", n)
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Give any erroneous extra wake a moment to (not) happen.
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(depthsSeen) != 3 {
		t.Fatalf("reactive chain should terminate at MaxDepth=3, got %d wakes (depths %v)", len(depthsSeen), depthsSeen)
	}
}

type dispatcherFunc func(ctx context.Context, agent, msg string) (string, error)

func (f dispatcherFunc) SendToAgent(ctx context.Context, agent, msg string) (string, error) {
	return f(ctx, agent, msg)
}

// AfterWake must fire with the agent's result so serve can consolidate the
// wake into memory — the self-learning write-back (§3.4).
func TestAfterWakeReceivesResult(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()

	type wake struct {
		agent  string
		typ    string
		result string
	}
	got := make(chan wake, 1)
	reg := fakeRegistry{m: map[string][]Trigger{"learner": {{On: "*", Prompt: "act"}}}}
	r := NewRouter(Config{
		Bus:      bus,
		Dispatch: dispatcherFunc(func(context.Context, string, string) (string, error) { return "did the thing", nil }),
		Triggers: reg,
		AfterWake: func(_ context.Context, agent string, e events.Event, result string) {
			got <- wake{agent, e.Type, result}
		},
	})
	r.Start(t.Context())
	bus.Publish(events.Event{Type: "signal.custom"})

	select {
	case w := <-got:
		if w.agent != "learner" || w.typ != "signal.custom" || w.result != "did the thing" {
			t.Fatalf("AfterWake got unexpected %+v", w)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AfterWake never fired — reactive wakes would not consolidate")
	}
}
