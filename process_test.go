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

// --- Process Linking Tests ---

func TestProcessLink(t *testing.T) {
	p1 := &Process{ID: "proc-1", Agent: &Agent{Name: "Agent1"}}
	p2 := &Process{ID: "proc-2", Agent: &Agent{Name: "Agent2"}}

	p1.Link(p2)

	// Check bidirectional link
	if len(p1.links) != 1 || p1.links["proc-2"] != p2 {
		t.Error("p1 should have p2 in its links")
	}
	if len(p2.links) != 1 || p2.links["proc-1"] != p1 {
		t.Error("p2 should have p1 in its links")
	}

	// Links() should return the linked process IDs
	links := p1.Links()
	if len(links) != 1 || links[0] != "proc-2" {
		t.Errorf("p1.Links() = %v, want [proc-2]", links)
	}
}

func TestProcessLinkSelf(t *testing.T) {
	p := &Process{ID: "proc-1"}
	p.Link(p) // Should be no-op

	if len(p.links) != 0 {
		t.Error("Process should not be able to link to itself")
	}
}

func TestProcessLinkNil(t *testing.T) {
	p := &Process{ID: "proc-1"}
	p.Link(nil) // Should be no-op

	if p.links != nil && len(p.links) != 0 {
		t.Error("Linking to nil should be a no-op")
	}
}

func TestProcessLinkIdempotent(t *testing.T) {
	p1 := &Process{ID: "proc-1"}
	p2 := &Process{ID: "proc-2"}

	p1.Link(p2)
	p1.Link(p2) // Link again

	if len(p1.links) != 1 {
		t.Error("Linking should be idempotent")
	}
}

func TestProcessUnlink(t *testing.T) {
	p1 := &Process{ID: "proc-1"}
	p2 := &Process{ID: "proc-2"}

	p1.Link(p2)
	p1.Unlink(p2)

	if len(p1.links) != 0 {
		t.Error("After unlink, p1 should have no links")
	}
	if len(p2.links) != 0 {
		t.Error("After unlink, p2 should have no links")
	}
}

func TestProcessUnlinkIdempotent(t *testing.T) {
	p1 := &Process{ID: "proc-1"}
	p2 := &Process{ID: "proc-2"}

	p1.Unlink(p2) // Unlink without linking first - should be no-op
	// No panic = success
}

func TestProcessMonitor(t *testing.T) {
	p1 := &Process{ID: "proc-1"}
	p2 := &Process{ID: "proc-2", Agent: &Agent{Name: "Agent2"}}

	ref := p1.Monitor(p2)

	if ref.processID != "proc-2" {
		t.Errorf("MonitorRef.processID = %q, want %q", ref.processID, "proc-2")
	}

	if len(p1.monitors) != 1 {
		t.Error("p1 should have p2 in its monitors")
	}
	if len(p2.monitoredBy) != 1 {
		t.Error("p2 should have p1 in its monitoredBy")
	}
	if p1.exitSignals == nil {
		t.Error("p1 should have exitSignals channel created")
	}
}

func TestProcessDemonitor(t *testing.T) {
	// Create orchestrator so Demonitor can find the other process
	o := NewOrchestrator(WithLLM(&mockLLM{}))

	p1 := &Process{ID: "proc-1", orchestrator: o}
	p2 := &Process{ID: "proc-2", orchestrator: o}

	// Register p2 in orchestrator
	o.mu.Lock()
	o.processes["proc-2"] = p2
	o.mu.Unlock()

	ref := p1.Monitor(p2)
	p1.Demonitor(ref)

	if len(p1.monitors) != 0 {
		t.Error("After demonitor, p1 should have no monitors")
	}
	if len(p2.monitoredBy) != 0 {
		t.Error("After demonitor, p2 should have no monitoredBy")
	}
}

func TestProcessTrapExit(t *testing.T) {
	p := &Process{ID: "proc-1"}

	if p.TrapExit() {
		t.Error("trapExit should default to false")
	}

	p.SetTrapExit(true)

	if !p.TrapExit() {
		t.Error("trapExit should be true after SetTrapExit(true)")
	}
	if p.exitSignals == nil {
		t.Error("exitSignals channel should be created when trapExit is set")
	}
}

func TestLinkedProcessDeath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p1 := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "Agent1"},
		status: StatusRunning,
		ctx:    ctx,
	}
	p2 := &Process{
		ID:     "proc-2",
		Agent:  &Agent{Name: "Agent2"},
		status: StatusRunning,
		ctx:    ctx,
	}

	p1.Link(p2)

	// p2 fails - p1 should also die
	p2.Fail(ErrTimeout)

	// Give propagation time to complete
	time.Sleep(10 * time.Millisecond)

	if p1.Status() != StatusFailed {
		t.Errorf("p1 status = %q, want %q (should die when linked process dies)", p1.Status(), StatusFailed)
	}
}

func TestLinkedProcessNormalExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p1 := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "Agent1"},
		status: StatusRunning,
		ctx:    ctx,
	}
	p2 := &Process{
		ID:     "proc-2",
		Agent:  &Agent{Name: "Agent2"},
		status: StatusRunning,
		ctx:    ctx,
	}

	p1.Link(p2)

	// p2 completes normally - p1 should NOT die
	p2.Complete("done")

	time.Sleep(10 * time.Millisecond)

	if p1.Status() != StatusRunning {
		t.Errorf("p1 status = %q, want %q (normal exit should not kill linked process)", p1.Status(), StatusRunning)
	}
}

func TestTrapExitReceivesSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p1 := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "Agent1"},
		status: StatusRunning,
		ctx:    ctx,
	}
	p2 := &Process{
		ID:     "proc-2",
		Agent:  &Agent{Name: "Agent2"},
		status: StatusRunning,
		ctx:    ctx,
	}

	p1.SetTrapExit(true)
	p1.Link(p2)

	// p2 fails - p1 should receive signal but NOT die
	p2.Fail(ErrTimeout)

	// Check that p1 is still running
	time.Sleep(10 * time.Millisecond)
	if p1.Status() != StatusRunning {
		t.Errorf("p1 status = %q, want %q (trapExit should prevent death)", p1.Status(), StatusRunning)
	}

	// Check that we received the exit signal
	select {
	case signal := <-p1.ExitSignals():
		if signal.ProcessID != "proc-2" {
			t.Errorf("signal.ProcessID = %q, want %q", signal.ProcessID, "proc-2")
		}
		if signal.Reason != ExitError {
			t.Errorf("signal.Reason = %q, want %q", signal.Reason, ExitError)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected to receive exit signal on exitSignals channel")
	}
}

func TestMonitorReceivesSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p1 := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "Agent1"},
		status: StatusRunning,
		ctx:    ctx,
	}
	p2 := &Process{
		ID:     "proc-2",
		Agent:  &Agent{Name: "Agent2"},
		status: StatusRunning,
		ctx:    ctx,
	}

	p1.Monitor(p2)

	// p2 fails - p1 should receive signal but NOT die (monitors don't cause death)
	p2.Fail(ErrTimeout)

	time.Sleep(10 * time.Millisecond)

	// p1 should still be running
	if p1.Status() != StatusRunning {
		t.Errorf("p1 status = %q, want %q (monitor should not cause death)", p1.Status(), StatusRunning)
	}

	// Check that we received the exit signal
	select {
	case signal := <-p1.ExitSignals():
		if signal.ProcessID != "proc-2" {
			t.Errorf("signal.ProcessID = %q, want %q", signal.ProcessID, "proc-2")
		}
		if signal.Reason != ExitError {
			t.Errorf("signal.Reason = %q, want %q", signal.Reason, ExitError)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected to receive exit signal from monitored process")
	}
}

func TestMonitorNormalCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p1 := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "Agent1"},
		status: StatusRunning,
		ctx:    ctx,
	}
	p2 := &Process{
		ID:     "proc-2",
		Agent:  &Agent{Name: "Agent2"},
		status: StatusRunning,
		ctx:    ctx,
	}

	p1.Monitor(p2)

	// p2 completes normally
	p2.Complete("success")

	// Check that we received the exit signal with normal reason
	select {
	case signal := <-p1.ExitSignals():
		if signal.Reason != ExitNormal {
			t.Errorf("signal.Reason = %q, want %q", signal.Reason, ExitNormal)
		}
		if signal.Result != "success" {
			t.Errorf("signal.Result = %q, want %q", signal.Result, "success")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected to receive exit signal from monitored process")
	}
}

func TestCascadingDeath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a chain: p1 <- p2 <- p3
	p1 := &Process{ID: "proc-1", Agent: &Agent{Name: "Agent1"}, status: StatusRunning, ctx: ctx}
	p2 := &Process{ID: "proc-2", Agent: &Agent{Name: "Agent2"}, status: StatusRunning, ctx: ctx}
	p3 := &Process{ID: "proc-3", Agent: &Agent{Name: "Agent3"}, status: StatusRunning, ctx: ctx}

	p1.Link(p2)
	p2.Link(p3)

	// p3 fails - should cascade to p2 and p1
	p3.Fail(ErrTimeout)

	time.Sleep(50 * time.Millisecond)

	if p2.Status() != StatusFailed {
		t.Errorf("p2 status = %q, want %q", p2.Status(), StatusFailed)
	}
	if p1.Status() != StatusFailed {
		t.Errorf("p1 status = %q, want %q", p1.Status(), StatusFailed)
	}
}

func TestLinkedProcessErrorType(t *testing.T) {
	err := &LinkedProcessError{
		LinkedID:      "proc-2",
		OriginalError: ErrTimeout,
	}

	if err.Error() != "linked process proc-2 died: operation timed out" {
		t.Errorf("LinkedProcessError.Error() = %q", err.Error())
	}

	if err.Unwrap() != ErrTimeout {
		t.Errorf("LinkedProcessError.Unwrap() = %v, want %v", err.Unwrap(), ErrTimeout)
	}
}

func TestLinkedProcessErrorNoOriginal(t *testing.T) {
	err := &LinkedProcessError{
		LinkedID:      "proc-2",
		OriginalError: nil,
	}

	if err.Error() != "linked process proc-2 died" {
		t.Errorf("LinkedProcessError.Error() = %q", err.Error())
	}
}

func TestExitReasonStrings(t *testing.T) {
	tests := []struct {
		reason ExitReason
		want   string
	}{
		{ExitNormal, "normal"},
		{ExitError, "error"},
		{ExitKilled, "killed"},
		{ExitLinked, "linked"},
	}

	for _, tt := range tests {
		if string(tt.reason) != tt.want {
			t.Errorf("ExitReason = %q, want %q", tt.reason, tt.want)
		}
	}
}
