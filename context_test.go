package vega

import (
	"context"
	"testing"

	"github.com/everydev1618/govega/llm"
)

func TestContextWithProcess(t *testing.T) {
	p := &Process{ID: "proc-1", Agent: &Agent{Name: "TestAgent"}}

	ctx := ContextWithProcess(context.Background(), p)
	got := ProcessFromContext(ctx)

	if got != p {
		t.Error("ProcessFromContext should return the process set with ContextWithProcess")
	}
}

func TestProcessFromContext_Empty(t *testing.T) {
	got := ProcessFromContext(context.Background())
	if got != nil {
		t.Error("ProcessFromContext on empty context should return nil")
	}
}

func TestContextWithEventSink(t *testing.T) {
	ch := make(chan<- ChatEvent, 1)

	ctx := ContextWithEventSink(context.Background(), ch)
	got := EventSinkFromContext(ctx)

	if got != ch {
		t.Error("EventSinkFromContext should return the channel set with ContextWithEventSink")
	}
}

func TestEventSinkFromContext_Empty(t *testing.T) {
	got := EventSinkFromContext(context.Background())
	if got != nil {
		t.Error("EventSinkFromContext on empty context should return nil")
	}
}

func TestProcessName(t *testing.T) {
	p := &Process{name: "worker-1"}
	if p.Name() != "worker-1" {
		t.Errorf("Name() = %q, want %q", p.Name(), "worker-1")
	}
}

func TestProcessGroups_Empty(t *testing.T) {
	p := &Process{}
	groups := p.Groups()
	if len(groups) != 0 {
		t.Errorf("Groups() on new process = %v, want empty", groups)
	}
}

func TestProcessSetExtraSystem(t *testing.T) {
	p := &Process{}
	p.SetExtraSystem("extra context info")

	p.mu.RLock()
	extra := p.extraSystem
	p.mu.RUnlock()

	if extra != "extra context info" {
		t.Errorf("extraSystem = %q, want %q", extra, "extra context info")
	}
}

func TestProcessCompleteResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		ctx:    ctx,
		cancel: cancel,
	}

	p.Complete("the result")

	if p.Status() != StatusCompleted {
		t.Errorf("Status = %q, want %q", p.Status(), StatusCompleted)
	}
	if p.Result() != "the result" {
		t.Errorf("Result = %q, want %q", p.Result(), "the result")
	}
}

func TestProcessCompleteIdempotent_Direct(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		ctx:    ctx,
		cancel: cancel,
	}

	p.Complete("first")
	p.Complete("second") // Should be no-op

	if p.Result() != "first" {
		t.Errorf("Result = %q, want %q (second Complete should be no-op)", p.Result(), "first")
	}
}

func TestProcessFail_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		ctx:    ctx,
		cancel: cancel,
	}

	p.Fail(ErrTimeout)
	p.Fail(ErrBudgetExceeded) // Should be no-op

	// Status should still be failed from first call
	if p.Status() != StatusFailed {
		t.Errorf("Status = %q, want %q", p.Status(), StatusFailed)
	}
}

func TestProcessStop_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Process{
		ID:     "proc-1",
		status: StatusCompleted,
		ctx:    ctx,
		cancel: cancel,
	}

	// Stop on completed process should be no-op
	p.Stop()
	if p.Status() != StatusCompleted {
		t.Errorf("Status = %q, want %q", p.Status(), StatusCompleted)
	}
}

func TestProcessMessages(t *testing.T) {
	p := &Process{
		Agent: &Agent{Name: "Test"},
	}

	msgs := p.Messages()
	if len(msgs) != 0 {
		t.Errorf("Messages on new process = %d, want 0", len(msgs))
	}
}

func TestProcessHydrateMessages(t *testing.T) {
	p := &Process{
		Agent:    &Agent{Name: "Test"},
		messages: make([]llm.Message, 0),
	}

	p.HydrateMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	})

	msgs := p.Messages()
	if len(msgs) != 2 {
		t.Fatalf("Messages after hydrate = %d, want 2", len(msgs))
	}
}

func TestProcessHydrateMessages_NoOpIfAlreadyHasMessages(t *testing.T) {
	p := &Process{
		Agent: &Agent{Name: "Test"},
		messages: []llm.Message{
			{Role: llm.RoleUser, Content: "existing"},
		},
	}

	p.HydrateMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "new"},
	})

	msgs := p.Messages()
	if len(msgs) != 1 || msgs[0].Content != "existing" {
		t.Error("HydrateMessages should be no-op when process already has messages")
	}
}
