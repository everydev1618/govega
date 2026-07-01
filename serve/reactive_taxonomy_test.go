package serve

import (
	"testing"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/events"
)

// The remember tool must emit memory.wrote onto the spine so agents can react
// to a memory being written.
func TestRememberEmitsMemoryWrote(t *testing.T) {
	doc, err := dsl.NewParser().Parse([]byte("name: t\nagents:\n  a:\n    model: m\n    system: s\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	interp, err := dsl.NewInterpreter(doc, dsl.WithLazySpawn())
	if err != nil {
		t.Fatalf("interp: %v", err)
	}

	got := make(chan events.Event, 4)
	interp.SetEventPublisher(func(e events.Event) { got <- e })
	RegisterMemoryTools(interp)

	// Invoke the remember tool with a memory context set, as a wake/chat would.
	ctx := ContextWithMemory(t.Context(), newTestStore(t), "default", "a")
	if _, err := interp.Tools().Execute(ctx, "remember", map[string]any{"content": "the user likes sushi", "type": "user"}); err != nil {
		t.Fatalf("remember: %v", err)
	}

	select {
	case e := <-got:
		if e.Type != "memory.wrote" {
			t.Fatalf("expected memory.wrote, got %q", e.Type)
		}
		if e.Data["agent"] != "a" || e.Data["type"] != "user" {
			t.Fatalf("memory.wrote data wrong: %v", e.Data)
		}
	default:
		t.Fatal("remember did not emit memory.wrote")
	}
}
