package serve

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
)

// scriptedLLM is a minimal LLM stub that returns a fixed `Generate` response.
// It also records what was asked of it so tests can assert prompt shape.
type scriptedLLM struct {
	response   string
	lastPrompt string
}

func (l *scriptedLLM) Generate(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	if len(messages) > 0 {
		l.lastPrompt = messages[len(messages)-1].Content
	}
	return &llm.LLMResponse{Content: l.response}, nil
}

func (l *scriptedLLM) GenerateStream(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		ch <- llm.StreamEvent{Delta: l.response}
		close(ch)
	}()
	return ch, nil
}

// reflectionPayload mirrors what the reflection prompt asks the LLM for, so
// tests can build well-formed responses without duplicating the wire format.
type reflectionPayload struct {
	Memories []reflectedMemoryEntry `json:"memories"`
}

func mustReflectionResponse(t *testing.T, p reflectionPayload) string {
	t.Helper()
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal reflection payload: %v", err)
	}
	return string(out)
}

// TestReflection_WritesTypedMemories verifies a happy-path reflection: the LLM
// returns two well-formed typed memories, and both end up in the store with
// the right type discriminator after `runReflection` returns.
func TestReflection_WritesTypedMemories(t *testing.T) {
	store := newTestStore(t)
	ll := &scriptedLLM{response: mustReflectionResponse(t, reflectionPayload{
		Memories: []reflectedMemoryEntry{
			{Type: string(MemoryTypeUser), Content: "Etienne is the CTO and writes Go.", Tags: "role"},
			{Type: string(MemoryTypeFeedback), Content: "Stop sending end-of-turn summaries.", Tags: "tone"},
		},
	})}

	if err := runReflection(context.Background(), ll, store, "u1", "apex", "what should you remember?", "ok"); err != nil {
		t.Fatalf("runReflection: %v", err)
	}

	got, err := store.SearchMemoryItems("u1", "apex", "Etienne", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(got) != 1 || got[0].Type != MemoryTypeUser {
		t.Fatalf("expected one user-typed memory matching 'Etienne', got %+v", got)
	}

	got, err = store.SearchMemoryItems("u1", "apex", "summaries", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(got) != 1 || got[0].Type != MemoryTypeFeedback {
		t.Fatalf("expected one feedback-typed memory matching 'summaries', got %+v", got)
	}
}

// TestReflection_DedupesAcrossRuns verifies that running reflection twice with
// the same content produces a single row — i.e. the upsert from #18 is
// engaged and the agent can over-reflect without polluting the store.
func TestReflection_DedupesAcrossRuns(t *testing.T) {
	store := newTestStore(t)
	resp := mustReflectionResponse(t, reflectionPayload{
		Memories: []reflectedMemoryEntry{
			{Type: string(MemoryTypeProject), Content: "App is Go + SQLite.", Tags: "stack"},
		},
	})
	ll := &scriptedLLM{response: resp}

	if err := runReflection(context.Background(), ll, store, "u1", "apex", "x", "y"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Sleep so updated_at can advance — proves dedup ran rather than missing entirely.
	time.Sleep(1100 * time.Millisecond)
	if err := runReflection(context.Background(), ll, store, "u1", "apex", "x", "y"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	got, err := store.SearchMemoryItems("u1", "apex", "Go + SQLite", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 row after two reflections, got %d", len(got))
	}
	if !got[0].UpdatedAt.After(got[0].CreatedAt) {
		t.Errorf("updated_at (%v) should have advanced past created_at (%v) on second run",
			got[0].UpdatedAt, got[0].CreatedAt)
	}
}

// TestReflection_SkipsInvalidType verifies that a single bad entry doesn't
// poison the whole run — valid siblings are still written.
func TestReflection_SkipsInvalidType(t *testing.T) {
	store := newTestStore(t)
	ll := &scriptedLLM{response: mustReflectionResponse(t, reflectionPayload{
		Memories: []reflectedMemoryEntry{
			{Type: "definitely-not-a-real-type", Content: "should be skipped"},
			{Type: string(MemoryTypeReference), Content: "valid reference content"},
		},
	})}

	if err := runReflection(context.Background(), ll, store, "u1", "apex", "x", "y"); err != nil {
		t.Fatalf("runReflection: %v", err)
	}

	got, err := store.SearchMemoryItems("u1", "apex", "valid reference", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the valid sibling to be saved (1 row), got %d", len(got))
	}

	bad, err := store.SearchMemoryItems("u1", "apex", "should be skipped", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("invalid-typed entry should NOT have been saved, got %d rows", len(bad))
	}
}

// TestReflection_SkipsMalformedJSON verifies that bogus LLM output doesn't
// crash the runner. The signal returned is a non-nil error so the caller
// can log it; nothing is written to the store.
func TestReflection_SkipsMalformedJSON(t *testing.T) {
	store := newTestStore(t)
	ll := &scriptedLLM{response: "not json, just prose explaining what i'd do"}

	if err := runReflection(context.Background(), ll, store, "u1", "apex", "x", "y"); err == nil {
		t.Fatal("runReflection should have returned a parse error for non-JSON input")
	}

	got, err := store.SearchMemoryItems("u1", "apex", "%", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("malformed reflection should have written nothing, got %d rows", len(got))
	}
}

// TestReflection_NoMemoriesNoOp verifies the empty-array case (the LLM
// honestly says "nothing worth remembering"). No error, no rows.
func TestReflection_NoMemoriesNoOp(t *testing.T) {
	store := newTestStore(t)
	ll := &scriptedLLM{response: mustReflectionResponse(t, reflectionPayload{
		Memories: []reflectedMemoryEntry{},
	})}

	if err := runReflection(context.Background(), ll, store, "u1", "apex", "x", "y"); err != nil {
		t.Fatalf("runReflection: %v", err)
	}

	got, err := store.SearchMemoryItems("u1", "apex", "%", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty reflection should have written nothing, got %d rows", len(got))
	}
}

// TestReflection_PromptCarriesContext is a sanity check that the runner
// passes user message + agent response into the prompt so the LLM has
// something to reflect over. Without this, reflection would be operating
// on whatever the model already knows, which defeats the point.
func TestReflection_PromptCarriesContext(t *testing.T) {
	store := newTestStore(t)
	ll := &scriptedLLM{response: mustReflectionResponse(t, reflectionPayload{Memories: nil})}

	const userMsg = "MARKER_USER_INPUT_xyz"
	const response = "MARKER_AGENT_RESPONSE_qwe"

	if err := runReflection(context.Background(), ll, store, "u1", "apex", userMsg, response); err != nil {
		t.Fatalf("runReflection: %v", err)
	}

	if !strings.Contains(ll.lastPrompt, userMsg) {
		t.Errorf("reflection prompt should include the user message; got: %q", ll.lastPrompt)
	}
	if !strings.Contains(ll.lastPrompt, response) {
		t.Errorf("reflection prompt should include the agent response; got: %q", ll.lastPrompt)
	}
}

// TestAgentReflectEnabled_Matrix covers the resolution rules between the
// global VEGA_REFLECTION env var and the per-agent memory.reflect flag:
//   - env="false"/"0"/"no" is a kill switch (always off, even if the agent
//     opted in).
//   - env="true"/"1"/"yes" is a global on (every agent reflects, even if
//     they didn't opt in).
//   - env unset / unrecognised defers to the per-agent flag.
//
// The kill-switch path is the safety valve operators reach for when an
// agent under load is generating noisy reflection writes; the global-on
// path is convenient during development before per-agent flags are tuned.
func TestAgentReflectEnabled_Matrix(t *testing.T) {
	reflectOn := &dsl.MemoryDef{Reflect: true}
	reflectOff := &dsl.MemoryDef{Reflect: false}

	cases := []struct {
		name   string
		env    string
		memory *dsl.MemoryDef
		want   bool
	}{
		{"env unset, no memory block", "", nil, false},
		{"env unset, memory.reflect: true", "", reflectOn, true},
		{"env unset, memory.reflect: false", "", reflectOff, false},
		{"env unset, unrecognised", "maybe", reflectOn, true},

		{"env=true, no memory block", "true", nil, true},
		{"env=TRUE, memory.reflect: false", "TRUE", reflectOff, true},
		{"env=1, no memory block", "1", nil, true},
		{"env=yes, no memory block", "yes", nil, true},

		{"env=false, memory.reflect: true (kill switch)", "false", reflectOn, false},
		{"env=0, memory.reflect: true (kill switch)", "0", reflectOn, false},
		{"env=no, memory.reflect: true (kill switch)", "no", reflectOn, false},
		{"env=false, no memory block", "false", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("VEGA_REFLECTION", c.env)
			if got := agentReflectEnabled(c.memory); got != c.want {
				t.Errorf("agentReflectEnabled(%v) under VEGA_REFLECTION=%q = %v, want %v",
					c.memory, c.env, got, c.want)
			}
		})
	}
}
