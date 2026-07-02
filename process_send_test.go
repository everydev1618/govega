package vega

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/memory"
)

// overlapLLM records how many Generate calls run concurrently.
type overlapLLM struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
}

func (m *overlapLLM) enter() {
	m.mu.Lock()
	m.inFlight++
	if m.inFlight > m.maxInFlight {
		m.maxInFlight = m.inFlight
	}
	m.mu.Unlock()
}

func (m *overlapLLM) exit() {
	m.mu.Lock()
	m.inFlight--
	m.mu.Unlock()
}

func (m *overlapLLM) max() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxInFlight
}

func (m *overlapLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	m.enter()
	time.Sleep(20 * time.Millisecond)
	m.exit()
	return &llm.LLMResponse{Content: "ok"}, nil
}

func (m *overlapLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		m.enter()
		time.Sleep(20 * time.Millisecond)
		m.exit()
		ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: "ok"}
		close(ch)
	}()
	return ch, nil
}

// errStreamLLM fails every call, streaming included.
type errStreamLLM struct{}

func (e *errStreamLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	return nil, errors.New("invalid request: boom")
}

func (e *errStreamLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Type: llm.StreamEventError, Error: errors.New("invalid request: boom")}
	close(ch)
	return ch, nil
}

// TestSendSerializedPerProcess verifies that concurrent Send calls on the
// same process are serialized: LLM calls never overlap and the conversation
// history alternates strictly user/assistant (M5).
func TestSendSerializedPerProcess(t *testing.T) {
	m := &overlapLLM{}
	o := NewOrchestrator(WithLLM(m))

	proc, err := o.Spawn(Agent{Name: "serial", System: StaticPrompt("test")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Complete("done")

	const sends = 5
	var wg sync.WaitGroup
	for i := 0; i < sends; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := proc.Send(context.Background(), fmt.Sprintf("msg-%d", i)); err != nil {
				t.Errorf("Send %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := m.max(); got > 1 {
		t.Errorf("concurrent Sends overlapped: max in-flight LLM calls = %d, want 1", got)
	}

	msgs := proc.Messages()
	if len(msgs) != sends*2 {
		t.Fatalf("history has %d messages, want %d", len(msgs), sends*2)
	}
	for i, msg := range msgs {
		want := llm.RoleUser
		if i%2 == 1 {
			want = llm.RoleAssistant
		}
		if msg.Role != want {
			t.Errorf("message %d has role %q, want %q (history interleaved)", i, msg.Role, want)
		}
	}
}

// TestSendStreamSerializedPerProcess verifies stream sends participate in the
// same per-process serialization as Send.
func TestSendStreamSerializedPerProcess(t *testing.T) {
	m := &overlapLLM{}
	o := NewOrchestrator(WithLLM(m))

	proc, err := o.Spawn(Agent{Name: "serial-stream", System: StaticPrompt("test")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Complete("done")

	const sends = 4
	var wg sync.WaitGroup
	for i := 0; i < sends; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stream, err := proc.SendStream(context.Background(), fmt.Sprintf("msg-%d", i))
			if err != nil {
				t.Errorf("SendStream %d: %v", i, err)
				return
			}
			for range stream.Chunks() {
			}
			if err := stream.Err(); err != nil {
				t.Errorf("stream %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := m.max(); got > 1 {
		t.Errorf("concurrent stream sends overlapped: max in-flight LLM calls = %d, want 1", got)
	}

	msgs := proc.Messages()
	if len(msgs) != sends*2 {
		t.Fatalf("history has %d messages, want %d", len(msgs), sends*2)
	}
	for i, msg := range msgs {
		want := llm.RoleUser
		if i%2 == 1 {
			want = llm.RoleAssistant
		}
		if msg.Role != want {
			t.Errorf("message %d has role %q, want %q (history interleaved)", i, msg.Role, want)
		}
	}
}

// TestSendRollsBackUserMessageOnError verifies the user message is removed
// from history when the LLM call fails, so the next Send doesn't produce
// two consecutive user messages (M5).
func TestSendRollsBackUserMessageOnError(t *testing.T) {
	o := NewOrchestrator(WithLLM(&errStreamLLM{}))

	proc, err := o.Spawn(Agent{Name: "rollback", System: StaticPrompt("test")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if _, err := proc.Send(context.Background(), "hello"); err == nil {
		t.Fatal("Send should have returned the LLM error")
	}

	if msgs := proc.Messages(); len(msgs) != 0 {
		t.Errorf("history not rolled back after LLM error: %d messages left, want 0 (first: %+v)", len(msgs), msgs[0])
	}
}

// TestSendRollbackMirrorsContextManager verifies rollback also removes the
// user message from the agent's ContextManager, which is what buildMessages
// actually sends to the LLM when Agent.Context is set.
func TestSendRollbackMirrorsContextManager(t *testing.T) {
	cm := memory.NewTokenBudgetContext(10000)
	o := NewOrchestrator(WithLLM(&errStreamLLM{}))

	proc, err := o.Spawn(Agent{Name: "rollback-ctx", System: StaticPrompt("test"), Context: cm})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if _, err := proc.Send(context.Background(), "hello"); err == nil {
		t.Fatal("Send should have returned the LLM error")
	}

	if msgs := cm.Messages(10000); len(msgs) != 0 {
		t.Errorf("context manager not rolled back after LLM error: %d messages left, want 0", len(msgs))
	}
}

// TestSendStreamRollsBackUserMessageOnError verifies the streaming path
// rolls back the user message on error too.
func TestSendStreamRollsBackUserMessageOnError(t *testing.T) {
	o := NewOrchestrator(WithLLM(&errStreamLLM{}))

	proc, err := o.Spawn(Agent{Name: "rollback-stream", System: StaticPrompt("test")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	stream, err := proc.SendStream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	for range stream.Chunks() {
	}
	if stream.Err() == nil {
		t.Fatal("stream should have surfaced the LLM error")
	}

	if msgs := proc.Messages(); len(msgs) != 0 {
		t.Errorf("history not rolled back after stream error: %d messages left, want 0", len(msgs))
	}
}
