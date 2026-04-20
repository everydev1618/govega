package dsl

import (
	"bytes"
	"strings"
	"testing"
)

func TestREPLQuit(t *testing.T) {
	doc := &Document{
		Name:   "test",
		Agents: map[string]*Agent{"alice": {Model: "test"}},
	}
	interp, err := NewInterpreter(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer interp.Shutdown()

	in := strings.NewReader("/quit\n")
	out := &bytes.Buffer{}

	repl := NewREPL(interp, WithREPLInput(in), WithREPLOutput(out))
	repl.Run()

	if !strings.Contains(out.String(), "Goodbye") {
		t.Errorf("expected goodbye message, got: %s", out.String())
	}
}

func TestREPLListAgents(t *testing.T) {
	doc := &Document{
		Name: "test",
		Agents: map[string]*Agent{
			"alice": {Model: "claude-sonnet-4-20250514"},
			"bob":   {Model: "claude-haiku-4-5-20251001"},
		},
	}
	interp, err := NewInterpreter(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer interp.Shutdown()

	in := strings.NewReader("/agents\n/quit\n")
	out := &bytes.Buffer{}

	repl := NewREPL(interp, WithREPLInput(in), WithREPLOutput(out))
	repl.Run()

	output := out.String()
	if !strings.Contains(output, "alice") {
		t.Errorf("expected alice in agents list, got: %s", output)
	}
	if !strings.Contains(output, "bob") {
		t.Errorf("expected bob in agents list, got: %s", output)
	}
}

func TestREPLAskUnknownAgent(t *testing.T) {
	doc := &Document{
		Name:   "test",
		Agents: map[string]*Agent{"alice": {Model: "test"}},
	}
	interp, err := NewInterpreter(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer interp.Shutdown()

	in := strings.NewReader("/ask nobody\n/quit\n")
	out := &bytes.Buffer{}

	repl := NewREPL(interp, WithREPLInput(in), WithREPLOutput(out))
	repl.Run()

	if !strings.Contains(out.String(), "not found") {
		t.Errorf("expected 'not found' error, got: %s", out.String())
	}
}

func TestREPLSingleAgentAutoSelect(t *testing.T) {
	doc := &Document{
		Name:   "test",
		Agents: map[string]*Agent{"colette": {Model: "test"}},
	}
	interp, err := NewInterpreter(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer interp.Shutdown()

	in := strings.NewReader("/quit\n")
	out := &bytes.Buffer{}

	repl := NewREPL(interp, WithREPLInput(in), WithREPLOutput(out))
	repl.Run()

	if !strings.Contains(out.String(), "[colette]") {
		t.Errorf("expected auto-selection of single agent, got: %s", out.String())
	}
}
