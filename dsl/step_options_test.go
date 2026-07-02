package dsl

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
)

// flakyLLM fails the first n Generate calls with a non-retryable error
// (so the LLM-layer retry doesn't mask the step-level behavior), then
// succeeds.
type flakyLLM struct {
	failures int32
	calls    atomic.Int32
}

func (f *flakyLLM) Generate(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	if f.calls.Add(1) <= f.failures {
		return nil, errors.New("invalid request: transient boom")
	}
	return &llm.LLMResponse{Content: "recovered"}, nil
}

func (f *flakyLLM) GenerateStream(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// blockingLLM never returns until the context is cancelled.
type blockingLLM struct{}

func (b *blockingLLM) Generate(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingLLM) GenerateStream(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

const stepOptionsYAML = `
name: step-options
agents:
  flaky:
    model: claude-sonnet-4-6
    system: flaky
workflows:
  retrying:
    steps:
      - flaky:
          send: "go"
          retry: 1
          save: result
    output: "{{result}}"
  slow:
    steps:
      - flaky:
          send: "go"
          timeout: 100ms
`

func stepOptionsInterp(t *testing.T, backend llm.LLM) *Interpreter {
	t.Helper()
	doc, err := NewParser().Parse([]byte(stepOptionsYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	interp, err := NewInterpreter(doc, WithLLM(backend))
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	return interp
}

// TestStepRetryEnforced verifies `retry: N` on a workflow step re-runs the
// step after a failure instead of being a parsed-but-ignored field.
func TestStepRetryEnforced(t *testing.T) {
	backend := &flakyLLM{failures: 1}
	interp := stepOptionsInterp(t, backend)

	result, err := interp.RunWorkflow(context.Background(), "retrying", map[string]any{})
	if err != nil {
		t.Fatalf("RunWorkflow should have retried past the first failure: %v", err)
	}
	if result != "recovered" {
		t.Errorf("result = %v, want recovered", result)
	}
	if got := backend.calls.Load(); got != 2 {
		t.Errorf("LLM called %d times, want 2 (fail + retry)", got)
	}
}

// TestStepRetryExhausted verifies the step still fails when retries run out.
func TestStepRetryExhausted(t *testing.T) {
	backend := &flakyLLM{failures: 10}
	interp := stepOptionsInterp(t, backend)

	if _, err := interp.RunWorkflow(context.Background(), "retrying", map[string]any{}); err == nil {
		t.Fatal("RunWorkflow should fail once retries are exhausted")
	}
	if got := backend.calls.Load(); got != 2 {
		t.Errorf("LLM called %d times, want 2 (retry: 1 = two attempts)", got)
	}
}

// TestStepTimeoutEnforced verifies `timeout:` on a workflow step bounds the
// step instead of letting it hang forever.
func TestStepTimeoutEnforced(t *testing.T) {
	interp := stepOptionsInterp(t, &blockingLLM{})

	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	start := time.Now()
	go func() {
		_, err := interp.RunWorkflow(context.Background(), "slow", map[string]any{})
		done <- outcome{err}
	}()

	select {
	case o := <-done:
		if o.err == nil {
			t.Fatal("workflow with a hung step should fail")
		}
		if !strings.Contains(o.err.Error(), "deadline") && !strings.Contains(o.err.Error(), "context") {
			t.Errorf("error = %v, want a timeout error", o.err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("step took %v to time out, want ~100ms", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("step timeout not enforced — workflow hung on a blocked LLM call")
	}
}
