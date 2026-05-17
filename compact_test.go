package vega

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/everydev1618/govega/llm"
)

// recordingLLM captures the messages each Generate call receives so
// tests can assert what was sent to the distiller. Returns a fixed
// response string.
type recordingLLM struct {
	mu       sync.Mutex
	response string
	seen     [][]llm.Message
}

func (r *recordingLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	r.seen = append(r.seen, cp)
	return &llm.LLMResponse{Content: r.response, InputTokens: 50, OutputTokens: 20}, nil
}

func (r *recordingLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Delta: r.response}
	close(ch)
	return ch, nil
}

// recordingSink captures the summary written by a Compact call.
type recordingSink struct {
	mu      sync.Mutex
	calls   int
	summary string
	meta    CompactionMeta
}

func (s *recordingSink) Write(ctx context.Context, summary string, meta CompactionMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.summary = summary
	s.meta = meta
	return nil
}

func spawnForCompact(t *testing.T, l llm.LLM) *Process {
	t.Helper()
	o := NewOrchestrator(WithLLM(l))
	p, err := o.Spawn(Agent{Name: "test-agent", System: StaticPrompt("you are a test")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return p
}

// fillMessages directly populates p.messages with N alternating
// user/assistant turns. Bypasses Send() so we don't need a working
// LLM loop just to exercise Compact's trim logic.
func fillMessages(p *Process, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := 0; i < n; i++ {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		p.messages = append(p.messages, llm.Message{
			Role:    role,
			Content: contentFor(role, i),
		})
	}
}

func contentFor(role llm.Role, i int) string {
	if role == llm.RoleUser {
		return "user msg " + itoa(i)
	}
	return "assistant msg " + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestCompact_SummarizesOldMessagesAndTrims(t *testing.T) {
	rec := &recordingLLM{response: "SUMMARY: the user introduced themselves and we discussed sushi."}
	p := spawnForCompact(t, rec)

	fillMessages(p, 20) // 20 messages, alternating user/assistant

	sink := &recordingSink{}
	err := p.Compact(context.Background(), 4, sink.Write)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Sink should have received the summary exactly once.
	if sink.calls != 1 {
		t.Fatalf("sink calls = %d, want 1", sink.calls)
	}
	if !strings.Contains(sink.summary, "SUMMARY") {
		t.Errorf("sink summary missing LLM output: %q", sink.summary)
	}
	if sink.meta.AgentName != "test-agent" {
		t.Errorf("meta.AgentName = %q, want test-agent", sink.meta.AgentName)
	}
	if sink.meta.DroppedCount <= 0 {
		t.Errorf("meta.DroppedCount = %d, want > 0", sink.meta.DroppedCount)
	}

	// LLM should have been called once with the dropped messages embedded
	// in the prompt.
	if len(rec.seen) != 1 {
		t.Fatalf("llm.Generate calls = %d, want 1", len(rec.seen))
	}
	prompt := rec.seen[0]
	flat := flattenContent(prompt)
	if !strings.Contains(flat, "user msg 0") {
		t.Errorf("distiller prompt missing oldest message; got: %s", flat)
	}

	// p.messages should now hold exactly the last 4 verbatim.
	p.mu.RLock()
	got := append([]llm.Message{}, p.messages...)
	p.mu.RUnlock()
	if len(got) != 4 {
		t.Fatalf("len(p.messages) after compact = %d, want 4", len(got))
	}
	if got[0].Content != contentFor(llm.RoleUser, 16) && got[0].Content != contentFor(llm.RoleAssistant, 16) {
		t.Errorf("first kept message = %q, want one of msg 16", got[0].Content)
	}
}

func TestCompact_NoOpWhenBelowKeepLast(t *testing.T) {
	rec := &recordingLLM{response: "should not be used"}
	p := spawnForCompact(t, rec)

	fillMessages(p, 3)

	sink := &recordingSink{}
	if err := p.Compact(context.Background(), 4, sink.Write); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if sink.calls != 0 {
		t.Errorf("sink should not be called when below threshold; calls = %d", sink.calls)
	}
	if len(rec.seen) != 0 {
		t.Errorf("llm should not be called; calls = %d", len(rec.seen))
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.messages) != 3 {
		t.Errorf("messages mutated; len = %d, want 3", len(p.messages))
	}
}

func TestCompact_KeepBoundaryFallsOnUserMessage(t *testing.T) {
	// Build 10 messages: u, a, u, a, ... so message[6] is user,
	// message[7] is assistant. With keepLast=4, naive trim would
	// start at index 6 (user) — fine. With keepLast=3, naive trim
	// starts at index 7 (assistant) — Compact should walk forward
	// to index 8 (user) so the kept window begins on a user turn.
	rec := &recordingLLM{response: "summary"}
	p := spawnForCompact(t, rec)
	fillMessages(p, 10)

	sink := &recordingSink{}
	if err := p.Compact(context.Background(), 3, sink.Write); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.messages) == 0 {
		t.Fatal("messages empty after compact")
	}
	if p.messages[0].Role != llm.RoleUser {
		t.Errorf("first kept role = %q, want user", p.messages[0].Role)
	}
}

func flattenContent(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
