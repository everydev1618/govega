package serve

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/llm"
)

// e2eLLM is a deterministic LLM fake that returns a fixed response and counts
// calls, so the e2e test can assert an agent actually woke and produced output.
type e2eLLM struct {
	mu       sync.Mutex
	calls    int
	response string
}

func (f *e2eLLM) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &llm.LLMResponse{Content: f.response}, nil
}

func (f *e2eLLM) GenerateStream(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	f.mu.Lock()
	f.calls++
	resp := f.response
	f.mu.Unlock()
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		ch <- llm.StreamEvent{Delta: resp}
		close(ch)
	}()
	return ch, nil
}

func (f *e2eLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestReactiveLoopEndToEnd drives the whole reactive slice through the real
// wiring: an event on the spine -> the router's gate -> a real interpreter
// SendToAgent (with a fake LLM for cognition) -> end-of-wake consolidation into
// the agent's memory. It is the repeatable regression check for "an agent wakes
// to an event with its memory and remembers having reacted."
func TestReactiveLoopEndToEnd(t *testing.T) {
	const yamlDoc = `
name: E2E Reactive
agents:
  watcher:
    model: claude-haiku-4-5
    system: You watch for signals and act on them.
    triggers:
      - on: signal.*
        prompt: "React to: {{.Data.detail}}"
`
	doc, err := dsl.NewParser().Parse([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	fake := &e2eLLM{response: "I handled the deploy signal."}
	interp, err := dsl.NewInterpreter(doc, dsl.WithLLM(fake), dsl.WithLazySpawn())
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}

	s := &Server{
		interp: interp,
		store:  newTestStore(t),
		bus:    events.NewBus(),
	}
	defer s.bus.Close()

	s.startReactive(t.Context())
	// Keep the consolidation note deterministic: use the templated body, not an
	// LLM-distilled one (distillation is covered separately and would make a
	// live model call here).
	s.reactiveDistiller = nil

	// Fire a signal the watcher subscribes to. In production this would come
	// from another agent's emit_event call or a completion; here we publish it
	// directly to exercise the spine → router → cognition → consolidation path.
	s.bus.Publish(events.Event{
		Type: "signal.deploy",
		Data: map[string]any{"detail": "production deploy finished"},
	})

	// The watcher should wake (fake LLM called) and, because the wake produced
	// output, consolidate a session note it will recall on its next wake.
	deadline := time.After(5 * time.Second)
	for {
		pages, err := s.store.ListMemoryPages(MemoryScopeAgent, "watcher", reactiveOwnerUserID, "sessions/reactive-")
		if err != nil {
			t.Fatalf("ListMemoryPages: %v", err)
		}
		if len(pages) > 0 {
			if !strings.Contains(pages[0].Content, "handled the deploy signal") {
				t.Fatalf("consolidation note missing the wake's outcome: %q", pages[0].Content)
			}
			if !strings.Contains(pages[0].Content, "signal.deploy") {
				t.Fatalf("consolidation note missing the triggering event: %q", pages[0].Content)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("watcher never woke+consolidated (LLM calls=%d)", fake.callCount())
		case <-time.After(25 * time.Millisecond):
		}
	}

	if fake.callCount() == 0 {
		t.Fatal("fake LLM was never called — the agent did not actually think")
	}
}
