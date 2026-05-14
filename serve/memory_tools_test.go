package serve

import (
	"context"
	"testing"
)

// TestCanonicalAgentName covers govega#83 (memory fragmentation across
// renamed/process-suffixed identities). The orchestrator rename from
// Iris → Charlie left memory rows scattered across `iris`, `Charlie`,
// `Charlie:1992054241`, and `charlie:1992054241` — none of which match
// the canonical `charlie` that current memory reads use as the key.
// canonicalAgentName collapses all of those to one stable identity.
func TestCanonicalAgentName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"charlie", "charlie"},
		{"Charlie", "charlie"},
		{"CHARLIE", "charlie"},
		{"Charlie:1992054241", "charlie"},
		{"charlie:1992054241", "charlie"},
		{"iris", "iris"},
		{"iris:abc", "iris"},
		{"mira", "mira"},
		{"", ""},
	}
	for _, c := range cases {
		if got := canonicalAgentName(c.in); got != c.want {
			t.Errorf("canonicalAgentName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestContextWithMemory_CanonicalizesAgent pins the contract that every
// memory tool reading the context sees the canonical agent name, regardless
// of what the caller passed. This is the seam that prevents future renames
// or PID-suffix leaks from fragmenting memory across identities again.
func TestContextWithMemory_CanonicalizesAgent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Charlie:1992054241", "charlie"},
		{"Charlie", "charlie"},
		{"charlie", "charlie"},
	}
	for _, c := range cases {
		ctx := ContextWithMemory(context.Background(), nil, "user1", c.in)
		// memoryFromContext requires a non-nil store; bypass it and read
		// the value directly — this asserts the context carries the
		// canonical form, which is what every downstream tool uses.
		got, _ := ctx.Value(memCtxAgent).(string)
		if got != c.want {
			t.Errorf("ContextWithMemory agent=%q → ctx agent = %q, want %q", c.in, got, c.want)
		}
	}
}
