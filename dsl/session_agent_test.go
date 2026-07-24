package dsl

import "testing"

// EnsureSessionAgent gives a base agent a per-session instance — its own
// spawned process registered under a composite "base:session" name — so that a
// guest-facing agent can hold an isolated conversation per session (chat
// history is keyed by agent name). It shallow-copies the base definition, is
// idempotent, and must never mutate the base agent.
func TestEnsureSessionAgent(t *testing.T) {
	yaml := `
name: session test
agents:
  ea:
    model: claude-sonnet-4-20250514
    system: You are the EA.
`
	doc, err := NewParser().Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	interp, err := NewInterpreter(doc, WithLazySpawn())
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	defer interp.Shutdown()

	if err := interp.EnsureSessionAgent("ea:stacie", "ea"); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}
	if !interp.HasAgent("ea:stacie") {
		t.Error("session agent ea:stacie should be registered")
	}

	// A different guest is an independent session agent.
	if err := interp.EnsureSessionAgent("ea:marco", "ea"); err != nil {
		t.Fatalf("second session: %v", err)
	}
	if !interp.HasAgent("ea:marco") {
		t.Error("session agent ea:marco should be registered")
	}

	// Idempotent: re-ensuring an existing session is a no-op, not an error.
	if err := interp.EnsureSessionAgent("ea:stacie", "ea"); err != nil {
		t.Errorf("EnsureSessionAgent must be idempotent; got %v", err)
	}

	// The session def is a distinct object; the base def is untouched.
	baseDef, ok := interp.agentDefLocked("ea")
	if !ok {
		t.Fatal("base def ea missing")
	}
	sessDef, ok := interp.agentDefLocked("ea:stacie")
	if !ok {
		t.Fatal("session def ea:stacie missing")
	}
	if baseDef == sessDef {
		t.Error("session agent must be a distinct def, not the base pointer")
	}
	if baseDef.System != "You are the EA." {
		t.Errorf("base def must not be mutated; System = %q", baseDef.System)
	}

	// Ensuring a session for an unknown base agent is an error.
	if err := interp.EnsureSessionAgent("nope:x", "nope"); err == nil {
		t.Error("expected error ensuring session for unknown base agent")
	}
}
