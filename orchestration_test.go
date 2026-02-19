package vega

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

// =============================================================================
// Mock Infrastructure for Agentic Testing
// =============================================================================

// toolCallingLLM simulates an LLM that makes tool calls
type toolCallingLLM struct {
	// responses is a queue of responses to return
	responses []*llm.LLMResponse
	// current index
	idx int
	mu  sync.Mutex
	// calls tracks all Generate calls made
	calls [][]llm.Message
	// generateDelay adds latency to simulate real LLM calls
	generateDelay time.Duration
}

func (m *toolCallingLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	if m.generateDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.generateDelay):
			// Continue after delay
		}
	}

	// Check context again after delay
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, messages)

	if m.idx >= len(m.responses) {
		return &llm.LLMResponse{Content: "default response", InputTokens: 10, OutputTokens: 5}, nil
	}

	resp := m.responses[m.idx]
	m.idx++
	return resp, nil
}

func (m *toolCallingLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 10)
	go func() {
		defer close(ch)
		resp, err := m.Generate(ctx, messages, tools)
		if err != nil {
			ch <- llm.StreamEvent{Error: err}
			return
		}
		ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: resp.Content}
	}()
	return ch, nil
}

// failingLLM returns errors to test fault tolerance
type failingLLM struct {
	failCount    int32 // number of times to fail before succeeding
	currentCount int32
	successResp  string
}

func (m *failingLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	count := atomic.AddInt32(&m.currentCount, 1)
	if count <= m.failCount {
		return nil, errors.New("simulated LLM failure")
	}
	return &llm.LLMResponse{
		Content:      m.successResp,
		InputTokens:  10,
		OutputTokens: 5,
	}, nil
}

func (m *failingLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		defer close(ch)
		resp, err := m.Generate(ctx, messages, tools)
		if err != nil {
			ch <- llm.StreamEvent{Error: err}
			return
		}
		ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: resp.Content}
	}()
	return ch, nil
}

// =============================================================================
// SUPERVISION & FAULT TOLERANCE TESTS
// =============================================================================

func TestSupervisionRecordFailure(t *testing.T) {
	t.Run("records failure and allows restart", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 3,
			Window:      time.Minute,
		}
		proc := &Process{ID: "test-1"}

		shouldRestart := sup.recordFailure(proc, errors.New("test error"))
		if !shouldRestart {
			t.Error("Expected shouldRestart=true for first failure")
		}
	})

	t.Run("respects max restarts limit", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 2,
			Window:      time.Minute,
		}
		proc := &Process{ID: "test-1"}

		// First two failures should allow restart
		sup.recordFailure(proc, errors.New("error 1"))
		sup.recordFailure(proc, errors.New("error 2"))

		// Third failure should not allow restart (exceeds max of 2)
		shouldRestart := sup.recordFailure(proc, errors.New("error 3"))
		if shouldRestart {
			t.Error("Expected shouldRestart=false after exceeding MaxRestarts")
		}
	})

	t.Run("stop strategy never restarts", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Stop,
			MaxRestarts: 10,
		}
		proc := &Process{ID: "test-1"}

		shouldRestart := sup.recordFailure(proc, errors.New("test error"))
		if shouldRestart {
			t.Error("Expected Stop strategy to never restart")
		}
	})

	t.Run("calls OnFailure callback", func(t *testing.T) {
		var called bool
		var receivedErr error
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 3,
			OnFailure: func(p *Process, err error) {
				called = true
				receivedErr = err
			},
		}
		proc := &Process{ID: "test-1"}
		testErr := errors.New("test error")

		sup.recordFailure(proc, testErr)

		if !called {
			t.Error("OnFailure callback was not called")
		}
		if receivedErr != testErr {
			t.Errorf("OnFailure received wrong error: got %v, want %v", receivedErr, testErr)
		}
	})

	t.Run("calls OnGiveUp when max restarts exceeded", func(t *testing.T) {
		var gaveUp bool
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 1,
			OnGiveUp: func(p *Process, err error) {
				gaveUp = true
			},
		}
		proc := &Process{ID: "test-1"}

		sup.recordFailure(proc, errors.New("error 1"))
		sup.recordFailure(proc, errors.New("error 2")) // Exceeds max

		if !gaveUp {
			t.Error("OnGiveUp was not called")
		}
	})

	t.Run("window prunes old failures", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 2,
			Window:      50 * time.Millisecond,
		}
		proc := &Process{ID: "test-1"}

		// Record two failures
		sup.recordFailure(proc, errors.New("error 1"))
		sup.recordFailure(proc, errors.New("error 2"))

		// Wait for window to expire
		time.Sleep(60 * time.Millisecond)

		// Third failure should be allowed (old ones pruned)
		shouldRestart := sup.recordFailure(proc, errors.New("error 3"))
		if !shouldRestart {
			t.Error("Expected restart after window expired")
		}
	})

	t.Run("unlimited restarts with MaxRestarts -1", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: -1, // Unlimited
		}
		proc := &Process{ID: "test-1"}

		// Should always allow restart
		for i := 0; i < 100; i++ {
			shouldRestart := sup.recordFailure(proc, errors.New("error"))
			if !shouldRestart {
				t.Errorf("Expected unlimited restarts, failed at iteration %d", i)
				break
			}
		}
	})
}

func TestSupervisionBackoff(t *testing.T) {
	t.Run("exponential backoff", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 10,
			Backoff: BackoffConfig{
				Initial:    100 * time.Millisecond,
				Multiplier: 2.0,
				Max:        1 * time.Second,
				Type:       BackoffExponential,
			},
		}
		proc := &Process{ID: "test-1"}

		// First restart: 100ms
		delay1 := sup.prepareRestart(proc)
		if delay1 != 100*time.Millisecond {
			t.Errorf("First delay = %v, want 100ms", delay1)
		}

		// Second restart: 200ms
		delay2 := sup.prepareRestart(proc)
		if delay2 != 200*time.Millisecond {
			t.Errorf("Second delay = %v, want 200ms", delay2)
		}

		// Third restart: 400ms
		delay3 := sup.prepareRestart(proc)
		if delay3 != 400*time.Millisecond {
			t.Errorf("Third delay = %v, want 400ms", delay3)
		}
	})

	t.Run("linear backoff", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 10,
			Backoff: BackoffConfig{
				Initial: 100 * time.Millisecond,
				Type:    BackoffLinear,
			},
		}
		proc := &Process{ID: "test-1"}

		delay1 := sup.prepareRestart(proc)
		delay2 := sup.prepareRestart(proc)
		delay3 := sup.prepareRestart(proc)

		if delay1 != 100*time.Millisecond {
			t.Errorf("First delay = %v, want 100ms", delay1)
		}
		if delay2 != 200*time.Millisecond {
			t.Errorf("Second delay = %v, want 200ms", delay2)
		}
		if delay3 != 300*time.Millisecond {
			t.Errorf("Third delay = %v, want 300ms", delay3)
		}
	})

	t.Run("constant backoff", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 10,
			Backoff: BackoffConfig{
				Initial: 100 * time.Millisecond,
				Type:    BackoffConstant,
			},
		}
		proc := &Process{ID: "test-1"}

		delay1 := sup.prepareRestart(proc)
		delay2 := sup.prepareRestart(proc)

		if delay1 != delay2 {
			t.Errorf("Constant backoff should not change: %v vs %v", delay1, delay2)
		}
	})

	t.Run("respects max delay", func(t *testing.T) {
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 10,
			Backoff: BackoffConfig{
				Initial:    100 * time.Millisecond,
				Multiplier: 10.0,
				Max:        500 * time.Millisecond,
				Type:       BackoffExponential,
			},
		}
		proc := &Process{ID: "test-1"}

		sup.prepareRestart(proc) // 100ms
		sup.prepareRestart(proc) // Would be 1000ms but capped

		delay := sup.prepareRestart(proc)
		if delay > 500*time.Millisecond {
			t.Errorf("Delay %v exceeded max of 500ms", delay)
		}
	})

	t.Run("calls OnRestart callback", func(t *testing.T) {
		var attempts []int
		sup := &Supervision{
			Strategy:    Restart,
			MaxRestarts: 5,
			OnRestart: func(p *Process, attempt int) {
				attempts = append(attempts, attempt)
			},
		}
		proc := &Process{ID: "test-1"}

		sup.prepareRestart(proc)
		sup.prepareRestart(proc)
		sup.prepareRestart(proc)

		if len(attempts) != 3 {
			t.Errorf("OnRestart called %d times, want 3", len(attempts))
		}
		if attempts[0] != 1 || attempts[1] != 2 || attempts[2] != 3 {
			t.Errorf("OnRestart attempts = %v, want [1,2,3]", attempts)
		}
	})
}

func TestSupervisionReset(t *testing.T) {
	sup := &Supervision{
		Strategy:    Restart,
		MaxRestarts: 2,
		Window:      time.Minute,
	}
	proc := &Process{ID: "test-1"}

	// Record some failures
	sup.recordFailure(proc, errors.New("error 1"))
	sup.prepareRestart(proc)
	sup.recordFailure(proc, errors.New("error 2"))

	// Reset
	sup.reset()

	// Should be able to fail again as if fresh
	shouldRestart := sup.recordFailure(proc, errors.New("error after reset"))
	if !shouldRestart {
		t.Error("Expected restart to be allowed after reset")
	}

	// Verify internal state is reset
	sup.mu.Lock()
	if sup.restarts != 0 {
		t.Errorf("restarts = %d, want 0 after reset", sup.restarts)
	}
	if len(sup.failures) != 1 { // The one failure we just recorded
		t.Errorf("failures = %d, want 1 after reset and one new failure", len(sup.failures))
	}
	sup.mu.Unlock()
}

func TestStrategyString(t *testing.T) {
	tests := []struct {
		strategy Strategy
		want     string
	}{
		{Restart, "restart"},
		{Stop, "stop"},
		{Escalate, "escalate"},
		{RestartAll, "restart_all"},
		{Strategy(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.strategy.String()
		if got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.strategy, got, tt.want)
		}
	}
}

// =============================================================================
// AGENTIC TOOL LOOP TESTS
// =============================================================================

func TestAgenticToolExecution(t *testing.T) {
	t.Run("executes tool and continues loop", func(t *testing.T) {
		// Create tools
		ts := tools.NewTools()
		var toolCalled bool
		ts.Register("get_weather", func(location string) string {
			toolCalled = true
			return "Sunny, 72°F"
		})

		// LLM that first calls a tool, then responds
		llm := &toolCallingLLM{
			responses: []*llm.LLMResponse{
				{
					Content: "Let me check the weather",
					ToolCalls: []llm.ToolCall{
						{ID: "call-1", Name: "get_weather", Arguments: map[string]any{"location": "Seattle"}},
					},
					InputTokens:  10,
					OutputTokens: 5,
				},
				{
					Content:      "The weather in Seattle is Sunny, 72°F",
					InputTokens:  15,
					OutputTokens: 10,
				},
			},
		}

		o := NewOrchestrator(WithLLM(llm))
		agent := Agent{Name: "weather-agent", Tools: ts}
		proc, err := o.Spawn(agent)
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}

		ctx := context.Background()
		response, err := proc.Send(ctx, "What's the weather in Seattle?")
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		if !toolCalled {
			t.Error("Tool was not called")
		}

		if response != "The weather in Seattle is Sunny, 72°F" {
			t.Errorf("Response = %q, want weather response", response)
		}

		// Verify metrics
		metrics := proc.Metrics()
		if metrics.ToolCalls != 1 {
			t.Errorf("ToolCalls = %d, want 1", metrics.ToolCalls)
		}
	})

	t.Run("handles multiple tool calls in sequence", func(t *testing.T) {
		ts := tools.NewTools()
		var callOrder []string
		ts.Register("tool_a", func(input string) string {
			callOrder = append(callOrder, "a")
			return "result_a"
		})
		ts.Register("tool_b", func(input string) string {
			callOrder = append(callOrder, "b")
			return "result_b"
		})

		llm := &toolCallingLLM{
			responses: []*llm.LLMResponse{
				{
					Content: "Calling tool A",
					ToolCalls: []llm.ToolCall{
						{ID: "call-1", Name: "tool_a", Arguments: map[string]any{"input": "test"}},
					},
				},
				{
					Content: "Now calling tool B",
					ToolCalls: []llm.ToolCall{
						{ID: "call-2", Name: "tool_b", Arguments: map[string]any{"input": "test"}},
					},
				},
				{
					Content: "Done with both tools",
				},
			},
		}

		o := NewOrchestrator(WithLLM(llm))
		agent := Agent{Name: "multi-tool-agent", Tools: ts}
		proc, _ := o.Spawn(agent)

		response, err := proc.Send(context.Background(), "Use both tools")
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		if len(callOrder) != 2 || callOrder[0] != "a" || callOrder[1] != "b" {
			t.Errorf("Call order = %v, want [a, b]", callOrder)
		}

		if response != "Done with both tools" {
			t.Errorf("Response = %q, want 'Done with both tools'", response)
		}
	})

	t.Run("handles tool errors gracefully", func(t *testing.T) {
		ts := tools.NewTools()
		ts.Register("failing_tool", tools.ToolDef{
			Description: "A tool that fails",
			Fn: func(ctx context.Context, params map[string]any) (string, error) {
				return "", errors.New("tool execution failed")
			},
			Params: map[string]tools.ParamDef{},
		})

		// LLM calls the failing tool, gets error, then responds
		callingLLM := &toolCallingLLM{
			responses: []*llm.LLMResponse{
				{
					Content: "Let me try this tool",
					ToolCalls: []llm.ToolCall{
						{ID: "call-1", Name: "failing_tool", Arguments: map[string]any{}},
					},
				},
				{
					Content: "The tool failed, but I handled it",
				},
			},
		}

		o := NewOrchestrator(WithLLM(callingLLM))
		agent := Agent{Name: "error-handler", Tools: ts}
		proc, _ := o.Spawn(agent)

		response, err := proc.Send(context.Background(), "Try the tool")
		if err != nil {
			t.Fatalf("Send should not fail even if tool fails: %v", err)
		}

		if response != "The tool failed, but I handled it" {
			t.Errorf("Response = %q, want error handling response", response)
		}

		// Verify error was sent back to LLM
		if len(callingLLM.calls) < 2 {
			t.Fatal("Expected at least 2 LLM calls")
		}
		lastCall := callingLLM.calls[1]
		foundErrorResult := false
		for _, msg := range lastCall {
			if msg.Role == llm.RoleUser && contains(msg.Content, "Error:") {
				foundErrorResult = true
				break
			}
		}
		if !foundErrorResult {
			t.Error("Tool error was not sent back to LLM")
		}
	})

	t.Run("tool with context receives valid context", func(t *testing.T) {
		ts := tools.NewTools()
		var receivedCtx context.Context
		ts.Register("ctx_tool", tools.ToolDef{
			Description: "Tool that checks context",
			Fn: func(ctx context.Context, params map[string]any) (string, error) {
				receivedCtx = ctx
				return "ok", nil
			},
			Params: map[string]tools.ParamDef{},
		})

		llm := &toolCallingLLM{
			responses: []*llm.LLMResponse{
				{
					Content:   "Calling context tool",
					ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "ctx_tool", Arguments: map[string]any{}}},
				},
				{Content: "Done"},
			},
		}

		o := NewOrchestrator(WithLLM(llm))
		agent := Agent{Name: "ctx-agent", Tools: ts}
		proc, _ := o.Spawn(agent)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		proc.Send(ctx, "test")

		if receivedCtx == nil {
			t.Error("Tool did not receive context")
		}
	})
}

func TestToolMiddleware(t *testing.T) {
	t.Run("middleware wraps tool execution", func(t *testing.T) {
		ts := tools.NewTools()
		var middlewareOrder []string

		ts.Use(func(next tools.ToolFunc) tools.ToolFunc {
			return func(ctx context.Context, params map[string]any) (string, error) {
				middlewareOrder = append(middlewareOrder, "before-outer")
				result, err := next(ctx, params)
				middlewareOrder = append(middlewareOrder, "after-outer")
				return result, err
			}
		})

		ts.Use(func(next tools.ToolFunc) tools.ToolFunc {
			return func(ctx context.Context, params map[string]any) (string, error) {
				middlewareOrder = append(middlewareOrder, "before-inner")
				result, err := next(ctx, params)
				middlewareOrder = append(middlewareOrder, "after-inner")
				return result, err
			}
		})

		ts.Register("test_tool", func(input string) string {
			middlewareOrder = append(middlewareOrder, "tool")
			return "result"
		})

		_, err := ts.Execute(context.Background(), "test_tool", map[string]any{"input": "test"})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		expected := []string{"before-outer", "before-inner", "tool", "after-inner", "after-outer"}
		if len(middlewareOrder) != len(expected) {
			t.Errorf("Middleware order = %v, want %v", middlewareOrder, expected)
		}
		for i, v := range expected {
			if middlewareOrder[i] != v {
				t.Errorf("Middleware order[%d] = %q, want %q", i, middlewareOrder[i], v)
			}
		}
	})
}

func TestToolSandbox(t *testing.T) {
	t.Run("rewrites paths within sandbox", func(t *testing.T) {
		ts := tools.NewTools(tools.WithSandbox("/sandbox"))
		var receivedPath string
		ts.Register("read_path", tools.ToolDef{
			Description: "Reads a path",
			Fn: func(ctx context.Context, params map[string]any) (string, error) {
				receivedPath = params["path"].(string)
				return "ok", nil
			},
			Params: map[string]tools.ParamDef{
				"path": {Type: "string", Required: true},
			},
		})

		ts.Execute(context.Background(), "read_path", map[string]any{"path": "test.txt"})

		if receivedPath != "/sandbox/test.txt" {
			t.Errorf("Path = %q, want /sandbox/test.txt", receivedPath)
		}
	})
}

// =============================================================================
// PROCESS LIFECYCLE & CALLBACKS TESTS
// =============================================================================

func TestProcessComplete(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	var completedProc *Process
	var completedResult string
	var mu sync.Mutex
	done := make(chan struct{})

	o.OnProcessComplete(func(p *Process, result string) {
		mu.Lock()
		completedProc = p
		completedResult = result
		mu.Unlock()
		close(done)
	})

	agent := Agent{Name: "test-agent"}
	proc, _ := o.Spawn(agent)

	proc.Complete("task finished successfully")

	// Wait for async callback
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for OnProcessComplete callback")
	}

	if proc.Status() != StatusCompleted {
		t.Errorf("Status = %q, want %q", proc.Status(), StatusCompleted)
	}

	if proc.Result() != "task finished successfully" {
		t.Errorf("Result = %q, want 'task finished successfully'", proc.Result())
	}

	mu.Lock()
	if completedProc != proc {
		t.Error("OnProcessComplete was not called with correct process")
	}

	if completedResult != "task finished successfully" {
		t.Errorf("OnProcessComplete result = %q, want 'task finished successfully'", completedResult)
	}
	mu.Unlock()
}

func TestProcessFail(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	var failedProc *Process
	var failedErr error
	var mu sync.Mutex
	done := make(chan struct{})

	o.OnProcessFailed(func(p *Process, err error) {
		mu.Lock()
		failedProc = p
		failedErr = err
		mu.Unlock()
		close(done)
	})

	agent := Agent{Name: "test-agent"}
	proc, _ := o.Spawn(agent)

	testErr := errors.New("something went wrong")
	proc.Fail(testErr)

	// Wait for async callback
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for OnProcessFailed callback")
	}

	if proc.Status() != StatusFailed {
		t.Errorf("Status = %q, want %q", proc.Status(), StatusFailed)
	}

	mu.Lock()
	if failedProc != proc {
		t.Error("OnProcessFailed was not called with correct process")
	}

	if failedErr != testErr {
		t.Errorf("OnProcessFailed error = %v, want %v", failedErr, testErr)
	}
	mu.Unlock()

	// Verify error count
	metrics := proc.Metrics()
	if metrics.Errors != 1 {
		t.Errorf("Errors = %d, want 1", metrics.Errors)
	}
}

func TestProcessStartedCallback(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	var startedProcs []*Process
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2) // Expecting 2 callbacks

	o.OnProcessStarted(func(p *Process) {
		mu.Lock()
		startedProcs = append(startedProcs, p)
		mu.Unlock()
		wg.Done()
	})

	agent := Agent{Name: "test-agent"}
	proc1, _ := o.Spawn(agent)
	proc2, _ := o.Spawn(agent)

	// Wait for async callbacks with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for OnProcessStarted callbacks")
	}

	mu.Lock()
	if len(startedProcs) != 2 {
		t.Errorf("OnProcessStarted called %d times, want 2", len(startedProcs))
	}

	// Verify both processes were reported
	found1, found2 := false, false
	for _, p := range startedProcs {
		if p == proc1 {
			found1 = true
		}
		if p == proc2 {
			found2 = true
		}
	}
	mu.Unlock()

	if !found1 || !found2 {
		t.Error("Not all spawned processes were reported to OnProcessStarted")
	}
}

func TestProcessCompleteIdempotent(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	callCount := int32(0)
	o.OnProcessComplete(func(p *Process, result string) {
		atomic.AddInt32(&callCount, 1)
	})

	agent := Agent{Name: "test-agent"}
	proc, _ := o.Spawn(agent)

	// Complete multiple times
	proc.Complete("result 1")
	proc.Complete("result 2")
	proc.Complete("result 3")

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("OnProcessComplete called %d times, want 1", callCount)
	}

	// First result should stick
	if proc.Result() != "result 1" {
		t.Errorf("Result = %q, want 'result 1'", proc.Result())
	}
}

func TestProcessFailIdempotent(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	callCount := int32(0)
	o.OnProcessFailed(func(p *Process, err error) {
		atomic.AddInt32(&callCount, 1)
	})

	agent := Agent{Name: "test-agent"}
	proc, _ := o.Spawn(agent)

	// Fail multiple times
	proc.Fail(errors.New("error 1"))
	proc.Fail(errors.New("error 2"))

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("OnProcessFailed called %d times, want 1", callCount)
	}
}

func TestMultipleCallbacks(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	var results []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2) // Expecting 2 callbacks

	// Register multiple callbacks
	o.OnProcessComplete(func(p *Process, result string) {
		mu.Lock()
		results = append(results, "callback1:"+result)
		mu.Unlock()
		wg.Done()
	})
	o.OnProcessComplete(func(p *Process, result string) {
		mu.Lock()
		results = append(results, "callback2:"+result)
		mu.Unlock()
		wg.Done()
	})

	agent := Agent{Name: "test-agent"}
	proc, _ := o.Spawn(agent)
	proc.Complete("done")

	// Wait for both callbacks with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for callbacks")
	}

	mu.Lock()
	if len(results) != 2 {
		t.Errorf("Got %d callback results, want 2", len(results))
	}
	mu.Unlock()
}

// =============================================================================
// HEALTH MONITORING TESTS
// =============================================================================

func TestHealthMonitorAlerts(t *testing.T) {
	t.Run("alerts on high cost", func(t *testing.T) {
		config := HealthConfig{
			CheckInterval: 10 * time.Millisecond,
			CostAlertUSD:  0.01,
		}
		monitor := NewHealthMonitor(config)

		proc := &Process{
			ID:     "test-proc",
			Agent:  &Agent{Name: "test-agent"},
			status: StatusRunning,
			metrics: ProcessMetrics{
				CostUSD: 0.02, // Over threshold
			},
		}

		getProcesses := func() []*Process { return []*Process{proc} }
		monitor.Start(getProcesses)
		defer monitor.Stop()

		select {
		case alert := <-monitor.Alerts():
			if alert.Type != AlertHighCost {
				t.Errorf("Alert type = %q, want %q", alert.Type, AlertHighCost)
			}
			if alert.ProcessID != "test-proc" {
				t.Errorf("Alert ProcessID = %q, want 'test-proc'", alert.ProcessID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected high cost alert, got none")
		}
	})

	t.Run("alerts on high iterations", func(t *testing.T) {
		config := HealthConfig{
			CheckInterval:        10 * time.Millisecond,
			MaxIterationsWarning: 5,
		}
		monitor := NewHealthMonitor(config)

		proc := &Process{
			ID:        "test-proc",
			Agent:     &Agent{Name: "test-agent"},
			status:    StatusRunning,
			iteration: 10, // Over threshold
			metrics: ProcessMetrics{
				Iterations: 10, // Health monitor checks metrics.Iterations
			},
		}

		getProcesses := func() []*Process { return []*Process{proc} }
		monitor.Start(getProcesses)
		defer monitor.Stop()

		select {
		case alert := <-monitor.Alerts():
			if alert.Type != AlertHighIterations {
				t.Errorf("Alert type = %q, want %q", alert.Type, AlertHighIterations)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected high iterations alert, got none")
		}
	})

	t.Run("alerts on error loop", func(t *testing.T) {
		config := HealthConfig{
			CheckInterval:  10 * time.Millisecond,
			ErrorLoopCount: 3,
		}
		monitor := NewHealthMonitor(config)

		proc := &Process{
			ID:     "test-proc",
			Agent:  &Agent{Name: "test-agent"},
			status: StatusRunning,
			metrics: ProcessMetrics{
				Errors: 5, // Over threshold
			},
		}

		getProcesses := func() []*Process { return []*Process{proc} }
		monitor.Start(getProcesses)
		defer monitor.Stop()

		select {
		case alert := <-monitor.Alerts():
			if alert.Type != AlertErrorLoop {
				t.Errorf("Alert type = %q, want %q", alert.Type, AlertErrorLoop)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected error loop alert, got none")
		}
	})

	t.Run("cleans up monitors for dead processes", func(t *testing.T) {
		config := HealthConfig{
			CheckInterval: 10 * time.Millisecond,
		}
		monitor := NewHealthMonitor(config)

		proc := &Process{
			ID:     "test-proc",
			Agent:  &Agent{Name: "test-agent"},
			status: StatusRunning,
		}

		var procs []*Process = []*Process{proc}
		var procsMu sync.Mutex
		getProcesses := func() []*Process {
			procsMu.Lock()
			defer procsMu.Unlock()
			// Return a copy to avoid race
			result := make([]*Process, len(procs))
			copy(result, procs)
			return result
		}
		monitor.Start(getProcesses)
		defer monitor.Stop()

		// Wait for monitor to pick up the process
		time.Sleep(20 * time.Millisecond)

		monitor.mu.Lock()
		if len(monitor.monitors) != 1 {
			t.Errorf("Expected 1 monitor, got %d", len(monitor.monitors))
		}
		monitor.mu.Unlock()

		// Remove the process
		procsMu.Lock()
		procs = []*Process{}
		procsMu.Unlock()

		// Wait for cleanup
		time.Sleep(20 * time.Millisecond)

		monitor.mu.Lock()
		if len(monitor.monitors) != 0 {
			t.Errorf("Expected 0 monitors after cleanup, got %d", len(monitor.monitors))
		}
		monitor.mu.Unlock()
	})
}

func TestOrchestratorHealthMonitoring(t *testing.T) {
	config := HealthConfig{
		CheckInterval: 10 * time.Millisecond,
		CostAlertUSD:  0.001,
	}

	llm := &mockLLM{response: "expensive operation"}
	o := NewOrchestrator(
		WithLLM(llm),
		WithHealthCheck(config),
	)

	var receivedAlert *Alert
	var alertMu sync.Mutex
	o.OnHealthAlert(func(alert Alert) {
		alertMu.Lock()
		receivedAlert = &alert
		alertMu.Unlock()
	})

	agent := Agent{Name: "test-agent"}
	proc, _ := o.Spawn(agent)

	// Simulate expensive operation
	proc.mu.Lock()
	proc.metrics.CostUSD = 0.01
	proc.mu.Unlock()

	// Wait for health check
	time.Sleep(50 * time.Millisecond)

	alertMu.Lock()
	if receivedAlert == nil {
		t.Error("Expected health alert, got none")
	} else if receivedAlert.Type != AlertHighCost {
		t.Errorf("Alert type = %q, want %q", receivedAlert.Type, AlertHighCost)
	}
	alertMu.Unlock()

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	o.Shutdown(ctx)
}

// =============================================================================
// CONCURRENCY TESTS
// =============================================================================

func TestConcurrentSpawn(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm), WithMaxProcesses(100))

	var wg sync.WaitGroup
	errors := make(chan error, 50)

	// Spawn 50 processes concurrently
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			agent := Agent{Name: fmt.Sprintf("agent-%d", n)}
			_, err := o.Spawn(agent)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Errorf("Got %d errors during concurrent spawn: %v", len(errs), errs)
	}

	procs := o.List()
	if len(procs) != 50 {
		t.Errorf("Expected 50 processes, got %d", len(procs))
	}
}

func TestConcurrentSend(t *testing.T) {
	llm := &toolCallingLLM{
		generateDelay: 5 * time.Millisecond,
	}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "concurrent-agent"}
	proc, _ := o.Spawn(agent)

	var wg sync.WaitGroup
	results := make(chan string, 10)
	errors := make(chan error, 10)

	// Send 10 messages concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := proc.Send(ctx, fmt.Sprintf("message %d", n))
			if err != nil {
				errors <- err
			} else {
				results <- resp
			}
		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Errorf("Got %d errors during concurrent send: %v", len(errs), errs)
	}

	resultCount := 0
	for range results {
		resultCount++
	}

	if resultCount != 10 {
		t.Errorf("Expected 10 results, got %d", resultCount)
	}
}

func TestConcurrentKill(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	// Spawn processes
	var procIDs []string
	for i := 0; i < 20; i++ {
		agent := Agent{Name: fmt.Sprintf("agent-%d", i)}
		proc, _ := o.Spawn(agent)
		procIDs = append(procIDs, proc.ID)
	}

	// Kill them all concurrently
	var wg sync.WaitGroup
	for _, id := range procIDs {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			o.Kill(pid)
		}(id)
	}

	wg.Wait()

	procs := o.List()
	if len(procs) != 0 {
		t.Errorf("Expected 0 processes after kill, got %d", len(procs))
	}
}

func TestProcessSendRaceCondition(t *testing.T) {
	// This test ensures that concurrent access to process state is safe
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "race-agent"}
	proc, _ := o.Spawn(agent)

	var wg sync.WaitGroup

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = proc.Status()
				_ = proc.Metrics()
				_ = proc.Result()
			}
		}()
	}

	// Concurrent writers via Send
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			proc.Send(ctx, fmt.Sprintf("message %d", n))
		}(i)
	}

	wg.Wait()
	// If we get here without a race detector warning, the test passes
}

// =============================================================================
// INTEGRATION TESTS
// =============================================================================

func TestEndToEndAgenticWorkflow(t *testing.T) {
	// Simulates a complete agentic workflow:
	// 1. Agent is spawned with tools
	// 2. User sends a task
	// 3. Agent uses tools to accomplish the task
	// 4. Agent reports completion

	ts := tools.NewTools()
	var taskCompleted atomic.Bool
	ts.Register("complete_task", tools.ToolDef{
		Description: "Mark a task as completed",
		Fn: func(ctx context.Context, params map[string]any) (string, error) {
			taskCompleted.Store(true)
			return "Task marked complete", nil
		},
		Params: map[string]tools.ParamDef{
			"task_id": {Type: "string", Required: true},
		},
	})

	llm := &toolCallingLLM{
		responses: []*llm.LLMResponse{
			{
				Content: "I'll complete this task",
				ToolCalls: []llm.ToolCall{
					{ID: "call-1", Name: "complete_task", Arguments: map[string]any{"task_id": "task-123"}},
				},
			},
			{
				Content: "The task has been completed successfully.",
			},
		},
	}

	o := NewOrchestrator(WithLLM(llm))

	// Track lifecycle with proper synchronization
	var started atomic.Bool
	startedCh := make(chan struct{})
	completedCh := make(chan string, 1)

	o.OnProcessStarted(func(p *Process) {
		if started.CompareAndSwap(false, true) {
			close(startedCh)
		}
	})

	o.OnProcessComplete(func(p *Process, result string) {
		completedCh <- result
	})

	agent := Agent{
		Name:   "task-agent",
		System: StaticPrompt("You are a task completion assistant."),
		Tools:  ts,
	}

	proc, err := o.Spawn(agent, WithTask("Complete task-123"))
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for started callback
	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for OnProcessStarted")
	}

	if !started.Load() {
		t.Error("OnProcessStarted was not called")
	}

	// Send the task
	ctx := context.Background()
	response, err := proc.Send(ctx, "Please complete task-123")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if !taskCompleted.Load() {
		t.Error("Task was not completed via tool")
	}

	if response != "The task has been completed successfully." {
		t.Errorf("Response = %q, want completion message", response)
	}

	// Mark process as complete
	proc.Complete(response)

	select {
	case completionResult := <-completedCh:
		if completionResult != response {
			t.Errorf("Completion result = %q, want %q", completionResult, response)
		}
	case <-time.After(time.Second):
		t.Error("OnProcessComplete was not called")
	}
}

func TestMultiAgentCoordination(t *testing.T) {
	// Tests multiple agents working together

	llm := &mockLLM{response: "coordinated response"}
	o := NewOrchestrator(WithLLM(llm))

	// Track all process events
	var startedCount, completedCount int32
	startedWg := sync.WaitGroup{}
	completedWg := sync.WaitGroup{}
	startedWg.Add(3)
	completedWg.Add(3)

	o.OnProcessStarted(func(p *Process) {
		atomic.AddInt32(&startedCount, 1)
		startedWg.Done()
	})

	o.OnProcessComplete(func(p *Process, result string) {
		atomic.AddInt32(&completedCount, 1)
		completedWg.Done()
	})

	// Spawn multiple agents
	agents := []Agent{
		{Name: "researcher"},
		{Name: "writer"},
		{Name: "reviewer"},
	}

	var procs []*Process
	for _, agent := range agents {
		proc, err := o.Spawn(agent)
		if err != nil {
			t.Fatalf("Failed to spawn %s: %v", agent.Name, err)
		}
		procs = append(procs, proc)
	}

	// Wait for all started callbacks
	startedDone := make(chan struct{})
	go func() {
		startedWg.Wait()
		close(startedDone)
	}()
	select {
	case <-startedDone:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for OnProcessStarted callbacks")
	}

	if atomic.LoadInt32(&startedCount) != 3 {
		t.Errorf("Started count = %d, want 3", startedCount)
	}

	// Each agent does work and completes
	for i, proc := range procs {
		ctx := context.Background()
		_, err := proc.Send(ctx, "Do your work")
		if err != nil {
			t.Errorf("Agent %d Send failed: %v", i, err)
		}
		proc.Complete("Work done")
	}

	// Wait for all completed callbacks
	completedDone := make(chan struct{})
	go func() {
		completedWg.Wait()
		close(completedDone)
	}()
	select {
	case <-completedDone:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for OnProcessComplete callbacks")
	}

	if atomic.LoadInt32(&completedCount) != 3 {
		t.Errorf("Completed count = %d, want 3", completedCount)
	}
}

func TestAgentWithTimeout(t *testing.T) {
	// LLM that takes too long
	llm := &toolCallingLLM{
		generateDelay: 500 * time.Millisecond,
	}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "slow-agent"}
	proc, _ := o.Spawn(agent)

	// Timeout is passed via context to Send, not via process options
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := proc.Send(ctx, "This will timeout")

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Error = %v, want context.DeadlineExceeded", err)
	}
}

func TestAgentContextCancellation(t *testing.T) {
	// Create an LLM that checks context cancellation during delay
	testLLM := &contextAwareLLM{
		delay: 500 * time.Millisecond,
	}
	o := NewOrchestrator(WithLLM(testLLM))

	agent := Agent{Name: "cancellable-agent"}
	proc, _ := o.Spawn(agent)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := proc.Send(ctx, "This will be cancelled")

	if err == nil {
		t.Error("Expected cancellation error, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Error = %v, want context.Canceled", err)
	}
}

// contextAwareLLM is an LLM that properly checks context during delays
type contextAwareLLM struct {
	delay time.Duration
}

func (m *contextAwareLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.delay):
		return &llm.LLMResponse{Content: "response", InputTokens: 10, OutputTokens: 5}, nil
	}
}

func (m *contextAwareLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		defer close(ch)
		resp, err := m.Generate(ctx, messages, tools)
		if err != nil {
			ch <- llm.StreamEvent{Error: err}
			return
		}
		ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: resp.Content}
	}()
	return ch, nil
}

func TestAsyncWorkflow(t *testing.T) {
	llm := &toolCallingLLM{
		generateDelay: 50 * time.Millisecond,
	}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "async-agent"}
	proc, _ := o.Spawn(agent)

	// Start async operation
	future := proc.SendAsync("async message")

	// Shouldn't be done immediately
	if future.Done() {
		t.Error("Future should not be done immediately")
	}

	// Wait for completion
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := future.Await(ctx)
	if err != nil {
		t.Fatalf("Await failed: %v", err)
	}

	if result == "" {
		t.Error("Expected non-empty result")
	}

	if !future.Done() {
		t.Error("Future should be done after Await")
	}
}

func TestStreamingWorkflow(t *testing.T) {
	testLLM := &streamingLLM{chunks: []string{"Hello", " ", "World", "!"}}
	o := NewOrchestrator(WithLLM(testLLM))

	agent := Agent{Name: "streaming-agent"}
	proc, _ := o.Spawn(agent)

	ctx := context.Background()
	stream, err := proc.SendStream(ctx, "stream this")
	if err != nil {
		t.Fatalf("SendStream failed: %v", err)
	}

	// Collect chunks
	var chunks []string
	for chunk := range stream.Chunks() {
		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	response := stream.Response()
	if response != "Hello World!" {
		t.Errorf("Response = %q, want 'Hello World!'", response)
	}

	if stream.Err() != nil {
		t.Errorf("Stream error = %v, want nil", stream.Err())
	}
}

// streamingLLM is an LLM that properly implements streaming
type streamingLLM struct {
	chunks []string
}

func (m *streamingLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	var content string
	for _, c := range m.chunks {
		content += c
	}
	return &llm.LLMResponse{Content: content, InputTokens: 10, OutputTokens: 5}, nil
}

func (m *streamingLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, len(m.chunks)+1)
	go func() {
		defer close(ch)
		for _, chunk := range m.chunks {
			ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: chunk}
		}
	}()
	return ch, nil
}

func TestDynamicSystemPrompt(t *testing.T) {
	callCount := 0
	dynamicPrompt := DynamicPrompt(func() string {
		callCount++
		return fmt.Sprintf("You are agent iteration %d", callCount)
	})

	llm := &toolCallingLLM{}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{
		Name:   "dynamic-agent",
		System: dynamicPrompt,
	}

	proc, _ := o.Spawn(agent)

	// First send
	ctx := context.Background()
	proc.Send(ctx, "first message")

	// Second send
	proc.Send(ctx, "second message")

	// Dynamic prompt should be called for each buildMessages
	if callCount < 2 {
		t.Errorf("Dynamic prompt called %d times, expected at least 2", callCount)
	}
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
