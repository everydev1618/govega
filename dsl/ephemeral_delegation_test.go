package dsl

import (
	"context"
	"strings"
	"sync"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

// recordingLLM captures the messages it receives on each call so tests can
// assert what was actually sent to the model.
type recordingLLM struct {
	mu       sync.Mutex
	calls    [][]llm.Message
	response string
}

func (r *recordingLLM) Generate(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	r.calls = append(r.calls, cp)
	return &llm.LLMResponse{Content: r.response}, nil
}

func (r *recordingLLM) GenerateStream(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	r.mu.Lock()
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	r.calls = append(r.calls, cp)
	resp := r.response
	r.mu.Unlock()
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		ch <- llm.StreamEvent{Delta: resp}
		close(ch)
	}()
	return ch, nil
}

func (r *recordingLLM) callsContaining(needle string) [][]llm.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]llm.Message
	for _, call := range r.calls {
		for _, msg := range call {
			if strings.Contains(msg.Content, needle) {
				out = append(out, call)
				break
			}
		}
	}
	return out
}

// newDelegationInterp builds a minimal Interpreter with two agents (parent and
// child) backed by the supplied LLM. Both agents share the same LLM, which is
// what we want when we're checking what messages the child process actually
// receives across successive delegations.
func newDelegationInterp(t *testing.T, ll llm.LLM) *Interpreter {
	t.Helper()

	yamlStr := `
version: 1
agents:
  parent:
    model: test
    system: I am parent.
  child:
    model: test
    system: I am child.
`
	doc, err := NewParser().Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	orch := vega.NewOrchestrator(vega.WithLLM(ll))
	t.Cleanup(func() { _ = orch.Shutdown(context.Background()) })

	toolSet := tools.NewTools()

	interp := &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             toolSet,
		delegationConfigs: make(map[string]*DelegationDef),
	}
	for name, def := range doc.Agents {
		if err := interp.spawnAgent(name, def); err != nil {
			t.Fatalf("spawnAgent(%s): %v", name, err)
		}
	}
	return interp
}

// TestSendToAgent_EphemeralProcessPerCall verifies that successive delegations
// to the same subagent do NOT accumulate conversation history. Each call must
// see only its own message — never the previous caller's message or the
// subagent's previous response.
//
// This guards against the "Riley sees every conversation Apex ever started
// with her" failure mode that surfaces once you have many subagents.
func TestSendToAgent_EphemeralProcessPerCall(t *testing.T) {
	rec := &recordingLLM{response: "ok"}
	interp := newDelegationInterp(t, rec)

	parentProc, err := interp.EnsureAgent("parent")
	if err != nil {
		t.Fatalf("EnsureAgent(parent): %v", err)
	}
	// Simulate being inside the parent's tool loop — that's the cue
	// SendToAgent uses to switch to ephemeral delegation.
	ctx := vega.ContextWithProcess(context.Background(), parentProc)

	if _, err := interp.SendToAgent(ctx, "child", "FIRST_DELEGATION"); err != nil {
		t.Fatalf("SendToAgent first: %v", err)
	}
	if _, err := interp.SendToAgent(ctx, "child", "SECOND_DELEGATION"); err != nil {
		t.Fatalf("SendToAgent second: %v", err)
	}

	secondCalls := rec.callsContaining("SECOND_DELEGATION")
	if len(secondCalls) == 0 {
		t.Fatal("recordingLLM saw no calls containing SECOND_DELEGATION")
	}

	// On the LLM call where the child receives SECOND_DELEGATION, the
	// prompt must NOT contain FIRST_DELEGATION — that would mean the child
	// process accumulated history across delegations.
	for _, call := range secondCalls {
		for _, msg := range call {
			if strings.Contains(msg.Content, "FIRST_DELEGATION") {
				t.Fatalf("child saw FIRST_DELEGATION in its prompt during the second delegation — process state leaked across calls.\nMessages:\n%s", formatMessages(call))
			}
		}
	}
}

func formatMessages(msgs []llm.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		b.WriteString("  [")
		b.WriteString(string(m.Role))
		b.WriteString("] ")
		// Trim very long contents for readability.
		c := m.Content
		if len(c) > 200 {
			c = c[:200] + "..."
		}
		b.WriteString(c)
		if i < len(msgs)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
