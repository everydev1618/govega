package serve

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func TestDiscordBotIDFromToken(t *testing.T) {
	// A Discord bot token is "<base64(userID)>.<base64(ts)>.<hmac>" where the
	// first segment base64-decodes to the bot's decimal snowflake id.
	id := "80351110224678912"
	seg := base64.RawStdEncoding.EncodeToString([]byte(id))
	token := seg + ".Gsomething.abcDEF123"

	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"valid", token, id},
		{"empty", "", ""},
		{"no dots", "notatoken", ""},
		{"non-numeric decode", base64.RawStdEncoding.EncodeToString([]byte("hello")) + ".x.y", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := discordBotIDFromToken(tc.token); got != tc.want {
				t.Fatalf("discordBotIDFromToken(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestSplitDiscordMessage(t *testing.T) {
	if got := splitDiscordMessage(""); got != nil {
		t.Fatalf("empty input: got %v, want nil", got)
	}

	short := "hello world"
	if got := splitDiscordMessage(short); len(got) != 1 || got[0] != short {
		t.Fatalf("short input not returned as single chunk: %v", got)
	}

	// Build a >2000-rune string with newline boundaries.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("0123456789\n") // 11 chars * 300 = 3300 runes
	}
	long := b.String()
	chunks := splitDiscordMessage(long)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c)); n > discordMaxMessageLen {
			t.Fatalf("chunk %d has %d runes, exceeds limit %d", i, n, discordMaxMessageLen)
		}
	}
	if strings.Join(chunks, "") != long {
		t.Fatal("rejoined chunks do not equal original — content was dropped or reordered")
	}

	// A single word longer than the limit must still be hard-split, not dropped.
	huge := strings.Repeat("x", 5000)
	hc := splitDiscordMessage(huge)
	if strings.Join(hc, "") != huge {
		t.Fatal("hard-split dropped content")
	}
	for _, c := range hc {
		if len([]rune(c)) > discordMaxMessageLen {
			t.Fatal("hard-split chunk exceeds limit")
		}
	}
}

func TestStripBotMention(t *testing.T) {
	id := "123"
	tests := []struct {
		in, want string
	}{
		{"<@123> hi there", "hi there"},
		{"<@!123> hi there", "hi there"},
		{"no mention here", "no mention here"},
		{"trailing <@123>", "trailing"},
	}
	for _, tc := range tests {
		if got := stripBotMention(tc.in, id); got != tc.want {
			t.Fatalf("stripBotMention(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Empty botID leaves content untouched.
	if got := stripBotMention("<@123> x", ""); got != "<@123> x" {
		t.Fatalf("empty botID should not strip: %q", got)
	}
}

func TestDiscordShouldRespond(t *testing.T) {
	if !discordShouldRespond(true, false) {
		t.Fatal("should respond to all DMs")
	}
	if !discordShouldRespond(false, true) {
		t.Fatal("should respond when mentioned in a guild")
	}
	if discordShouldRespond(false, false) {
		t.Fatal("should NOT respond to unmentioned guild messages")
	}
}

// discordTestServer builds the minimum a *Server needs for Discord persistence
// tests: a real store and an interpreter holding the given agents.
func discordTestServer(t *testing.T, agents map[string]*dsl.Agent) *Server {
	t.Helper()
	if agents == nil {
		agents = map[string]*dsl.Agent{}
	}
	doc := &dsl.Document{
		Agents:   agents,
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	return &Server{
		store:    newTestStore(t),
		interp:   interp,
		streams:  map[string]*activeStream{},
		discords: map[string]*runningDiscordBot{},
		cfg:      Config{Orchestrator: dsl.IrisConfig{Name: "iris"}},
	}
}

func TestDiscordPersistenceRoundTrip(t *testing.T) {
	s := discordTestServer(t, nil)

	cfgs := []DiscordBotConfig{
		{ID: "111", Token: "111tok", Agent: "iris", Label: "Personal"},
		{ID: "222", Token: "222tok", Agent: "iris"},
	}
	if err := s.savePersistedDiscordBots(cfgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := s.loadPersistedDiscordBots()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 2 || loaded[0].ID != "111" || loaded[1].ID != "222" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}

	// Snapshot must never leak tokens and must mark all as not-running
	// (nothing was actually started).
	snap := s.DiscordBotsSnapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snap))
	}
	for _, st := range snap {
		if st.Running {
			t.Fatalf("bot %s reported running but none were started", st.ID)
		}
	}

	// Remove drops the entry from persistence.
	if err := s.RemoveDiscordBot("111"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	loaded, _ = s.loadPersistedDiscordBots()
	if len(loaded) != 1 || loaded[0].ID != "222" {
		t.Fatalf("after remove: %+v", loaded)
	}
}

func TestAddDiscordBotValidation(t *testing.T) {
	s := discordTestServer(t, map[string]*dsl.Agent{"iris": {Name: "iris"}})

	// Malformed token → error before any network/session work.
	if _, err := s.AddDiscordBot(nil, "not-a-valid-token", "iris", ""); err == nil {
		t.Fatal("expected error for malformed token")
	}

	// Valid-looking token but unknown agent → error, and nothing persisted
	// (rolled back). The agent-existence check runs before the session opens.
	seg := base64.RawStdEncoding.EncodeToString([]byte("999"))
	token := seg + ".ts.hmac"
	if _, err := s.AddDiscordBot(nil, token, "ghost", ""); err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if loaded, _ := s.loadPersistedDiscordBots(); len(loaded) != 0 {
		t.Fatalf("unknown-agent add should not persist, got %+v", loaded)
	}
}
