package vega

import (
	"context"
	"testing"
	"time"
)

// mockLLM is a simple mock for testing
type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &LLMResponse{
		Content:      m.response,
		InputTokens:  10,
		OutputTokens: 5,
		CostUSD:      0.001,
	}, nil
}

func (m *mockLLM) GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	go func() {
		ch <- StreamEvent{Delta: m.response}
		close(ch)
	}()
	return ch, nil
}

func TestNewOrchestrator(t *testing.T) {
	o := NewOrchestrator()
	if o == nil {
		t.Fatal("NewOrchestrator() returned nil")
	}

	if o.maxProcesses != 100 {
		t.Errorf("Default maxProcesses = %d, want 100", o.maxProcesses)
	}

	if len(o.processes) != 0 {
		t.Errorf("New orchestrator should have 0 processes, got %d", len(o.processes))
	}
}

func TestWithMaxProcesses(t *testing.T) {
	o := NewOrchestrator(WithMaxProcesses(50))
	if o.maxProcesses != 50 {
		t.Errorf("maxProcesses = %d, want 50", o.maxProcesses)
	}
}

func TestWithLLM(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))
	if o.defaultLLM != llm {
		t.Error("defaultLLM was not set correctly")
	}
}

func TestSpawn(t *testing.T) {
	llm := &mockLLM{response: "Hello!"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{
		Name:   "test-agent",
		Model:  "test-model",
		System: StaticPrompt("You are a test agent."),
	}

	proc, err := o.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn() returned error: %v", err)
	}

	if proc == nil {
		t.Fatal("Spawn() returned nil process")
	}

	if proc.ID == "" {
		t.Error("Process should have an ID")
	}

	if proc.Agent.Name != "test-agent" {
		t.Errorf("Process.Agent.Name = %q, want %q", proc.Agent.Name, "test-agent")
	}

	if proc.Status() != StatusRunning {
		t.Errorf("Process.Status() = %q, want %q", proc.Status(), StatusRunning)
	}
}

func TestSpawnWithTask(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "test"}
	proc, err := o.Spawn(agent, WithTask("Build a REST API"))
	if err != nil {
		t.Fatalf("Spawn() returned error: %v", err)
	}

	if proc.Task != "Build a REST API" {
		t.Errorf("Process.Task = %q, want %q", proc.Task, "Build a REST API")
	}
}

func TestSpawnWithWorkDir(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "test"}
	proc, err := o.Spawn(agent, WithWorkDir("/tmp/test"))
	if err != nil {
		t.Fatalf("Spawn() returned error: %v", err)
	}

	if proc.WorkDir != "/tmp/test" {
		t.Errorf("Process.WorkDir = %q, want %q", proc.WorkDir, "/tmp/test")
	}
}

func TestSpawnMaxProcesses(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm), WithMaxProcesses(2))

	agent := Agent{Name: "test"}

	// Spawn 2 processes (should succeed)
	_, err := o.Spawn(agent)
	if err != nil {
		t.Fatalf("First Spawn() returned error: %v", err)
	}

	_, err = o.Spawn(agent)
	if err != nil {
		t.Fatalf("Second Spawn() returned error: %v", err)
	}

	// Third should fail
	_, err = o.Spawn(agent)
	if err != ErrMaxProcessesReached {
		t.Errorf("Third Spawn() error = %v, want ErrMaxProcessesReached", err)
	}
}

func TestSpawnWithoutLLM(t *testing.T) {
	o := NewOrchestrator() // No LLM configured

	agent := Agent{Name: "test"}
	_, err := o.Spawn(agent)
	if err == nil {
		t.Error("Spawn() without LLM should return error")
	}
}

func TestGet(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "test"}
	proc, _ := o.Spawn(agent)

	// Get existing process
	got := o.Get(proc.ID)
	if got != proc {
		t.Error("Get() did not return the spawned process")
	}

	// Get non-existent process
	got = o.Get("nonexistent")
	if got != nil {
		t.Error("Get() for non-existent ID should return nil")
	}
}

func TestList(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	// Empty list
	procs := o.List()
	if len(procs) != 0 {
		t.Errorf("List() on empty orchestrator = %d processes, want 0", len(procs))
	}

	// Spawn some processes
	agent := Agent{Name: "test"}
	o.Spawn(agent)
	o.Spawn(agent)

	procs = o.List()
	if len(procs) != 2 {
		t.Errorf("List() = %d processes, want 2", len(procs))
	}
}

func TestKill(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "test"}
	proc, _ := o.Spawn(agent)
	id := proc.ID

	// Kill existing process
	err := o.Kill(id)
	if err != nil {
		t.Errorf("Kill() returned error: %v", err)
	}

	// Process should be removed
	if o.Get(id) != nil {
		t.Error("Process should be removed after Kill()")
	}

	// Kill non-existent process
	err = o.Kill("nonexistent")
	if err != ErrProcessNotFound {
		t.Errorf("Kill() for non-existent ID = %v, want ErrProcessNotFound", err)
	}
}

func TestShutdown(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "test"}
	o.Spawn(agent)
	o.Spawn(agent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := o.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() returned error: %v", err)
	}
}

func TestShutdownTimeout(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := o.Shutdown(ctx)
	if err != context.Canceled {
		t.Errorf("Shutdown() with cancelled context = %v, want context.Canceled", err)
	}
}

func TestWithRateLimits(t *testing.T) {
	limits := map[string]RateLimitConfig{
		"claude-3": {
			RequestsPerMinute: 60,
			TokensPerMinute:   100000,
			Strategy:          RateLimitQueue,
		},
	}

	o := NewOrchestrator(WithRateLimits(limits))
	if len(o.rateLimits) != 1 {
		t.Errorf("rateLimits count = %d, want 1", len(o.rateLimits))
	}
}

func TestRateLimitStrategy(t *testing.T) {
	tests := []struct {
		strategy RateLimitStrategy
		want     RateLimitStrategy
	}{
		{RateLimitQueue, 0},
		{RateLimitReject, 1},
		{RateLimitBackpressure, 2},
	}

	for _, tt := range tests {
		if tt.strategy != tt.want {
			t.Errorf("RateLimitStrategy = %d, want %d", tt.strategy, tt.want)
		}
	}
}

func TestWithSupervision(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	sup := Supervision{
		Strategy:    Restart,
		MaxRestarts: 3,
	}

	agent := Agent{Name: "test"}
	proc, err := o.Spawn(agent, WithSupervision(sup))
	if err != nil {
		t.Fatalf("Spawn() returned error: %v", err)
	}

	if proc.Supervision == nil {
		t.Error("Process.Supervision should not be nil")
	}

	if proc.Supervision.MaxRestarts != 3 {
		t.Errorf("Process.Supervision.MaxRestarts = %d, want 3", proc.Supervision.MaxRestarts)
	}
}

func TestWithTimeout(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	agent := Agent{Name: "test"}
	proc, err := o.Spawn(agent, WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("Spawn() returned error: %v", err)
	}

	// The context should have a deadline
	deadline, ok := proc.ctx.Deadline()
	if !ok {
		t.Error("Process context should have a deadline")
	}

	// Deadline should be roughly 5 seconds from now
	remaining := time.Until(deadline)
	if remaining < 4*time.Second || remaining > 6*time.Second {
		t.Errorf("Context deadline remaining = %v, want ~5s", remaining)
	}
}

func TestWithProcessContext(t *testing.T) {
	llm := &mockLLM{response: "test"}
	o := NewOrchestrator(WithLLM(llm))

	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	agent := Agent{Name: "test"}
	proc, err := o.Spawn(agent, WithProcessContext(parentCtx))
	if err != nil {
		t.Fatalf("Spawn() returned error: %v", err)
	}

	// Cancel parent should affect child
	parentCancel()

	select {
	case <-proc.ctx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Process context should be cancelled when parent is cancelled")
	}
}
