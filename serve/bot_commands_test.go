package serve

import (
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func TestBotCommandParsing(t *testing.T) {
	cases := map[string]string{
		"/agents":         "agents",
		"agents":          "agents",
		"  Agents ":       "agents",
		"/team":           "agents",
		"who":             "agents",
		"/channels":       "channels",
		"channels":        "channels",
		"/status":         "status",
		"status":          "status",
		"activity":        "status",
		"build me a game": "",
		"list the agents": "", // only a bare command matches, not prose
		"":                "",
	}
	for in, want := range cases {
		if got := botCommand(in); got != want {
			t.Errorf("botCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

const botCmdDoc = `
name: cmdtest
settings:
  default_model: claude-sonnet-4-6
agents:
  tony:
    model: claude-sonnet-4-6
    system: "You are the orchestrator."
    is_meta: true
  sage:
    model: claude-sonnet-4-6
    system: "Brand designer who crafts visual identity."
`

func newBotCommandFixture(t *testing.T) *botExchange {
	t.Helper()
	doc, err := dsl.NewParser().Parse([]byte(botCmdDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	interp, err := dsl.NewInterpreter(doc, dsl.WithLazySpawn())
	if err != nil {
		t.Fatalf("interp: %v", err)
	}
	t.Cleanup(interp.Shutdown)
	return &botExchange{interp: interp, store: newTestStore(t), baseAgent: "tony", surface: surfaceDiscord}
}

func TestFormatAgentsCommand(t *testing.T) {
	b := newBotCommandFixture(t)
	out := b.formatCommand("agents", "default")
	// Lists every defined agent with its role, and marks the orchestrator.
	for _, want := range []string{"sage", "Brand designer", "tony", "orchestrator"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatChannelsCommand(t *testing.T) {
	b := newBotCommandFixture(t)
	if err := b.store.CreateChannel("ch1", "content", "blog drafts", "system", []string{"sage", "river"}, ""); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	out := b.formatCommand("channels", "default")
	if !strings.Contains(out, "content") {
		t.Errorf("channels output missing channel name:\n%s", out)
	}
}

func TestFormatStatusCommand(t *testing.T) {
	b := newBotCommandFixture(t)
	out := b.formatCommand("status", "default")
	if out == "" {
		t.Error("status command returned empty output")
	}
}

func TestDispatchProgress(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"send_to_agent", map[string]any{"agent": "sage"}, "→ handing this to **sage**…"},
		{"delegate", map[string]any{"agent": "river"}, "→ handing this to **river**…"},
		{"deploy_app", map[string]any{"name": "pacman"}, "→ hosting **pacman**…"},
		{"read_file", map[string]any{"path": "x"}, ""}, // routine tools are silent
		{"exec", map[string]any{"command": "ls"}, ""},
		{"send_to_agent", map[string]any{}, ""}, // missing agent → nothing
	}
	for _, c := range cases {
		if got := dispatchProgress(c.tool, c.args); got != c.want {
			t.Errorf("dispatchProgress(%q,%v) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}
