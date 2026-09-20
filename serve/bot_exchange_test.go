package serve

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
)

// botLLM is a minimal llm.LLM fake: records every Generate call and
// replies with a fixed string.
type botLLM struct {
	mu    sync.Mutex
	calls [][]llm.Message
	reply string
}

func (m *botLLM) record(messages []llm.Message) *llm.LLMResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	m.calls = append(m.calls, cp)
	reply := m.reply
	if reply == "" {
		reply = "ok"
	}
	return &llm.LLMResponse{Content: reply, StopReason: llm.StopReasonEnd}
}

func (m *botLLM) Generate(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	return m.record(messages), nil
}

func (m *botLLM) GenerateStream(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	resp := m.record(messages)
	ch := make(chan llm.StreamEvent, 8)
	ch <- llm.StreamEvent{Type: llm.StreamEventMessageStart, InputTokens: 1}
	ch <- llm.StreamEvent{Type: llm.StreamEventContentStart}
	ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: resp.Content}
	ch <- llm.StreamEvent{Type: llm.StreamEventContentEnd}
	ch <- llm.StreamEvent{Type: llm.StreamEventMessageEnd, OutputTokens: 1, StopReason: resp.StopReason}
	close(ch)
	return ch, nil
}

// lastCall returns the message list of the most recent LLM call.
func (m *botLLM) lastCall(t *testing.T) []llm.Message {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		t.Fatal("LLM was never called")
	}
	return m.calls[len(m.calls)-1]
}

const botExchangeDoc = `
name: bottest
settings:
  default_model: claude-sonnet-4-6
agents:
  tony:
    model: claude-sonnet-4-6
    system: "MARKER-TONY: you are tony"
`

func newBotExchangeFixture(t *testing.T, backend *botLLM) (*dsl.Interpreter, *SQLiteStore) {
	t.Helper()
	doc, err := dsl.NewParser().Parse([]byte(botExchangeDoc))
	if err != nil {
		t.Fatalf("parse doc: %v", err)
	}
	interp, err := dsl.NewInterpreter(doc, dsl.WithLLM(backend))
	if err != nil {
		t.Fatalf("interpreter: %v", err)
	}
	return interp, newTestStore(t)
}

// A fresh process (server restart, failed-turn respawn) must be rehydrated
// from persisted chat history before the turn runs, so the bot doesn't
// greet the user as a stranger. This is the jackal-game amnesia bug: the
// web path hydrates, the bot paths didn't.
func TestBotExchangeHydratesProcessFromHistory(t *testing.T) {
	backend := &botLLM{}
	interp, store := newBotExchangeFixture(t, backend)

	// Persisted history from "before the restart".
	mustInsert := func(role, content string) {
		t.Helper()
		if err := store.InsertChatMessage("tony", role, content, nil); err != nil {
			t.Fatalf("insert %s message: %v", role, err)
		}
	}
	mustInsert("user", "share the game link")
	mustInsert("assistant", "here it is: https://example.com/jackal.html")

	exch := &botExchange{
		interp:    interp,
		store:     store,
		surface:   surfaceDiscord,
		baseAgent: "tony",
	}
	if _, err := exch.run(context.Background(), "tony", "what was that link again?", "snowflake-123", nil, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	// The LLM must have seen the pre-restart turns ahead of the new question.
	msgs := backend.lastCall(t)
	var sawLink bool
	for _, m := range msgs {
		if strings.Contains(m.Content, "https://example.com/jackal.html") {
			sawLink = true
			break
		}
	}
	if !sawLink {
		t.Fatalf("persisted history was not hydrated into the process; LLM saw %d messages without the link", len(msgs))
	}
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleUser || last.Content != "what was that link again?" {
		t.Fatalf("last message should be the new question, got role=%s content=%q", last.Role, last.Content)
	}
}

// Both sides of a bot exchange must be persisted to chat history under the
// routed agent, same as the web path.
func TestBotExchangePersistsExchange(t *testing.T) {
	backend := &botLLM{reply: "hello there"}
	interp, store := newBotExchangeFixture(t, backend)

	exch := &botExchange{interp: interp, store: store, surface: surfaceTelegram, baseAgent: "tony"}
	if _, err := exch.run(context.Background(), "tony", "hi", "12345", nil, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	history, err := store.ListChatMessages("tony")
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hi" {
		t.Fatalf("first message: got %s %q", history[0].Role, history[0].Content)
	}
	if history[1].Role != "assistant" || history[1].Content != "hello there" {
		t.Fatalf("second message: got %s %q", history[1].Role, history[1].Content)
	}
}

// Memory must be scoped to the same identity the web surface uses —
// "default" when no resolver attaches claims — NOT the platform user id
// (Discord snowflake / Telegram numeric id). Otherwise each surface gets a
// disjoint memory namespace and cross-surface recall silently fails.
func TestBotExchangeMemoryScopedToDefaultUser(t *testing.T) {
	backend := &botLLM{}
	interp, store := newBotExchangeFixture(t, backend)

	// Wiki memory written under the web surface's identity.
	if err := store.UpsertMemoryPage(MemoryPage{
		Scope:   MemoryScopeUser,
		ScopeID: "default",
		UserID:  "default",
		Path:    "MEMORY.md",
		Content: "- jackal game lives at https://example.com/jackal.html",
	}); err != nil {
		t.Fatalf("seed memory page: %v", err)
	}

	exch := &botExchange{interp: interp, store: store, surface: surfaceDiscord, baseAgent: "tony"}
	if _, err := exch.run(context.Background(), "tony", "what do you remember?", "snowflake-987654", nil, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	msgs := backend.lastCall(t)
	if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
		t.Fatal("expected a system message first")
	}
	// Memory rides in the volatile half of the system prompt (it is
	// per-process context), so assert against the whole thing.
	if system := msgs[0].SystemText(); !strings.Contains(system, "jackal game lives at") {
		t.Fatalf("web-scoped (\"default\") wiki memory was not injected on the bot surface; system prompt:\n%s", system)
	}
}

// onExchange must fire after a successful turn, with the memory-scoped
// user id (claims when a resolver attaches them, else "default") — this is
// the hook the server wires to the memory curator.
func TestBotExchangeFiresOnExchange(t *testing.T) {
	backend := &botLLM{reply: "noted"}
	interp, store := newBotExchangeFixture(t, backend)

	var (
		gotUser, gotAgent, gotMsg, gotResp string
		fired                              bool
	)
	exch := &botExchange{
		interp:    interp,
		store:     store,
		surface:   surfaceDiscord,
		baseAgent: "tony",
		onExchange: func(ctx context.Context, userID, agent, userMsg, response string) {
			fired = true
			gotUser, gotAgent, gotMsg, gotResp = userID, agent, userMsg, response
		},
	}
	if _, err := exch.run(context.Background(), "tony", "remember the link", "snowflake-1", nil, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !fired {
		t.Fatal("onExchange did not fire after a successful exchange")
	}
	if gotUser != "default" {
		t.Fatalf("onExchange userID = %q, want \"default\" (memory-scoped, not the platform id)", gotUser)
	}
	if gotAgent != "tony" || gotMsg != "remember the link" || gotResp != "noted" {
		t.Fatalf("onExchange got (%q, %q, %q), want (tony, remember the link, noted)", gotAgent, gotMsg, gotResp)
	}
}

// When a CallerResolver attaches claims, the claims identity wins over the
// "default" fallback — multi-user products keep per-user memory.
func TestBotExchangeResolverClaimsScopeMemory(t *testing.T) {
	backend := &botLLM{}
	interp, store := newBotExchangeFixture(t, backend)

	var gotUser string
	exch := &botExchange{
		interp:    interp,
		store:     store,
		surface:   surfaceTelegram,
		baseAgent: "tony",
		resolver: func(ctx context.Context, userID string) context.Context {
			return WithClaims(ctx, AuthClaims{UserID: "apex-user-42"})
		},
		onExchange: func(ctx context.Context, userID, agent, userMsg, response string) {
			gotUser = userID
		},
	}
	if _, err := exch.run(context.Background(), "tony", "hi", "tg-777", nil, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotUser != "apex-user-42" {
		t.Fatalf("memory userID = %q, want resolver-claims identity \"apex-user-42\"", gotUser)
	}
}

// A failed turn must not fire onExchange and must not persist a phantom
// assistant message.
func TestBotExchangeErrorDoesNotFireOnExchange(t *testing.T) {
	backend := &botLLM{}
	interp, store := newBotExchangeFixture(t, backend)

	fired := false
	exch := &botExchange{
		interp:    interp,
		store:     store,
		surface:   surfaceDiscord,
		baseAgent: "tony",
		onExchange: func(ctx context.Context, userID, agent, userMsg, response string) {
			fired = true
		},
	}
	// Unknown agent → EnsureAgent and SendToAgent both fail.
	if _, err := exch.run(context.Background(), "nosuchagent", "hi", "u1", nil, nil); err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if fired {
		t.Fatal("onExchange fired on a failed exchange")
	}
}

func TestMemoryUserID(t *testing.T) {
	if got := memoryUserID(context.Background()); got != "default" {
		t.Fatalf("no claims: got %q, want \"default\"", got)
	}
	ctx := WithClaims(context.Background(), AuthClaims{UserID: "u-9"})
	if got := memoryUserID(ctx); got != "u-9" {
		t.Fatalf("with claims: got %q, want \"u-9\"", got)
	}
	// Claims present but empty UserID falls back to default.
	ctx = WithClaims(context.Background(), AuthClaims{})
	if got := memoryUserID(ctx); got != "default" {
		t.Fatalf("empty claims: got %q, want \"default\"", got)
	}
}
