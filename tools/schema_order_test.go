package tools

import (
	"context"
	"testing"
)

// registerNamed registers no-op tools under the given names, in order.
func registerNamed(t *testing.T, ts *Tools, names ...string) {
	t.Helper()
	for _, n := range names {
		name := n
		err := ts.Register(name, ToolDef{
			Description: "desc " + name,
			Params:      map[string]ParamDef{},
			Fn: ToolFunc(func(ctx context.Context, _ map[string]any) (string, error) {
				return name, nil
			}),
		})
		if err != nil {
			t.Fatalf("Register(%q): %v", name, err)
		}
	}
}

func schemaOrder(ts *Tools) []string {
	out := make([]string, 0)
	for _, s := range ts.Schema() {
		out = append(out, s.Name)
	}
	return out
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Tool order is the first thing Anthropic hashes for a prompt-cache prefix:
// a reordered tools array invalidates system and messages behind it. Go
// randomizes map iteration, so Schema() must not read the map directly.
func TestSchemaKeepsRegistrationOrder(t *testing.T) {
	ts := NewTools()
	want := []string{
		"search_members", "search_threads", "read_thread", "community_stats",
		"remember", "forget", "upcoming_events", "share_clip", "start_pairing",
		"billing_status",
	}
	registerNamed(t, ts, want...)

	if got := schemaOrder(ts); !equalNames(got, want) {
		t.Fatalf("Schema() = %v, want registration order %v", got, want)
	}
}

func TestSchemaOrderIsStableAcrossCalls(t *testing.T) {
	ts := NewTools()
	registerNamed(t, ts, "alpha", "bravo", "charlie", "delta", "echo",
		"foxtrot", "golf", "hotel", "india", "juliet")

	first := schemaOrder(ts)
	for i := 0; i < 50; i++ {
		if got := schemaOrder(ts); !equalNames(got, first) {
			t.Fatalf("call %d: Schema() = %v, want %v", i, got, first)
		}
	}
}

// A filtered view is what an agent actually sends, so it has to be stable too
// — and in the parent's registration order, not the caller's argument order.
func TestFilterKeepsRegistrationOrder(t *testing.T) {
	ts := NewTools()
	registerNamed(t, ts, "alpha", "bravo", "charlie", "delta")

	sub := ts.Filter("delta", "bravo")
	want := []string{"bravo", "delta"}
	for i := 0; i < 20; i++ {
		if got := schemaOrder(sub); !equalNames(got, want) {
			t.Fatalf("Filter().Schema() = %v, want %v", got, want)
		}
	}
}
