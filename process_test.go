package vega

import (
	"context"
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusTimeout, "timeout"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("Status = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestProcessStatus(t *testing.T) {
	p := &Process{
		status: StatusRunning,
	}

	if got := p.Status(); got != StatusRunning {
		t.Errorf("Process.Status() = %q, want %q", got, StatusRunning)
	}
}

func TestProcessMetrics(t *testing.T) {
	now := time.Now()
	p := &Process{
		metrics: ProcessMetrics{
			Iterations:   5,
			InputTokens:  1000,
			OutputTokens: 500,
			CostUSD:      0.05,
			StartedAt:    now,
			ToolCalls:    3,
			Errors:       1,
		},
	}

	metrics := p.Metrics()

	if metrics.Iterations != 5 {
		t.Errorf("ProcessMetrics.Iterations = %d, want %d", metrics.Iterations, 5)
	}

	if metrics.InputTokens != 1000 {
		t.Errorf("ProcessMetrics.InputTokens = %d, want %d", metrics.InputTokens, 1000)
	}

	if metrics.CostUSD != 0.05 {
		t.Errorf("ProcessMetrics.CostUSD = %f, want %f", metrics.CostUSD, 0.05)
	}
}

func TestProcessStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Process{
		status: StatusRunning,
		ctx:    ctx,
		cancel: cancel,
	}

	p.Stop()

	if p.status != StatusCompleted {
		t.Errorf("After Stop(), status = %q, want %q", p.status, StatusCompleted)
	}

	if p.metrics.CompletedAt.IsZero() {
		t.Error("After Stop(), CompletedAt should be set")
	}
}

func TestFutureDone(t *testing.T) {
	f := &Future{
		done:      make(chan struct{}),
		cancel:    make(chan struct{}),
		completed: false,
	}

	if f.Done() {
		t.Error("Future.Done() should be false before completion")
	}

	f.completed = true
	if !f.Done() {
		t.Error("Future.Done() should be true after completion")
	}
}

func TestFutureResult(t *testing.T) {
	f := &Future{
		done:      make(chan struct{}),
		cancel:    make(chan struct{}),
		completed: false,
	}

	// Before completion
	_, err := f.Result()
	if err != ErrNotCompleted {
		t.Errorf("Future.Result() before completion should return ErrNotCompleted, got %v", err)
	}

	// After completion
	f.result = "test result"
	f.completed = true

	result, err := f.Result()
	if err != nil {
		t.Errorf("Future.Result() after completion returned error: %v", err)
	}
	if result != "test result" {
		t.Errorf("Future.Result() = %q, want %q", result, "test result")
	}
}

func TestFutureAwait(t *testing.T) {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}

	// Complete the future in a goroutine
	go func() {
		time.Sleep(10 * time.Millisecond)
		f.mu.Lock()
		f.result = "awaited result"
		f.completed = true
		f.mu.Unlock()
		close(f.done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := f.Await(ctx)
	if err != nil {
		t.Errorf("Future.Await() returned error: %v", err)
	}
	if result != "awaited result" {
		t.Errorf("Future.Await() = %q, want %q", result, "awaited result")
	}
}

func TestFutureAwaitTimeout(t *testing.T) {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := f.Await(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Future.Await() with timeout should return DeadlineExceeded, got %v", err)
	}
}

func TestFutureCancel(t *testing.T) {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}

	// Cancel should not block
	done := make(chan struct{})
	go func() {
		f.Cancel()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("Future.Cancel() should not block")
	}
}

func TestStreamChunks(t *testing.T) {
	s := &Stream{
		chunks: make(chan string, 3),
		done:   make(chan struct{}),
	}

	// Send some chunks
	s.chunks <- "Hello"
	s.chunks <- " "
	s.chunks <- "World"
	close(s.chunks)

	chunks := s.Chunks()
	var result string
	for chunk := range chunks {
		result += chunk
	}

	if result != "Hello World" {
		t.Errorf("Stream chunks = %q, want %q", result, "Hello World")
	}
}

func TestStreamResponse(t *testing.T) {
	s := &Stream{
		chunks:   make(chan string),
		done:     make(chan struct{}),
		response: "complete response",
	}

	close(s.done) // Simulate completion

	if got := s.Response(); got != "complete response" {
		t.Errorf("Stream.Response() = %q, want %q", got, "complete response")
	}
}

func TestStreamErr(t *testing.T) {
	s := &Stream{
		chunks: make(chan string),
		done:   make(chan struct{}),
		err:    ErrProcessNotRunning,
	}

	close(s.done) // Simulate completion

	if got := s.Err(); got != ErrProcessNotRunning {
		t.Errorf("Stream.Err() = %v, want %v", got, ErrProcessNotRunning)
	}
}

func TestCallMetrics(t *testing.T) {
	cm := CallMetrics{
		InputTokens:  100,
		OutputTokens: 50,
		CostUSD:      0.001,
		LatencyMs:    250,
		ToolCalls:    []string{"read_file", "write_file"},
		Retries:      1,
	}

	if cm.InputTokens != 100 {
		t.Errorf("CallMetrics.InputTokens = %d, want %d", cm.InputTokens, 100)
	}

	if len(cm.ToolCalls) != 2 {
		t.Errorf("len(CallMetrics.ToolCalls) = %d, want %d", len(cm.ToolCalls), 2)
	}
}

func TestSendResult(t *testing.T) {
	result := SendResult{
		Response: "Test response",
		Error:    nil,
		Metrics: CallMetrics{
			InputTokens: 50,
		},
	}

	if result.Response != "Test response" {
		t.Errorf("SendResult.Response = %q, want %q", result.Response, "Test response")
	}

	if result.Error != nil {
		t.Errorf("SendResult.Error = %v, want nil", result.Error)
	}
}

func TestFormatToolResult(t *testing.T) {
	result := formatToolResult("call-123", "read_file", "file contents here")
	expected := `<tool_result tool_use_id="call-123" name="read_file">
file contents here
</tool_result>`

	if result != expected {
		t.Errorf("formatToolResult() = %q, want %q", result, expected)
	}
}
