package vega

import (
	"testing"
	"time"
)

func TestStrategyStringValues(t *testing.T) {
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
		if got := tt.strategy.String(); got != tt.want {
			t.Errorf("Strategy(%d).String() = %q, want %q", tt.strategy, got, tt.want)
		}
	}
}

func TestSupervisionRecordFailure_StopStrategy(t *testing.T) {
	s := &Supervision{Strategy: Stop, MaxRestarts: 5}

	shouldRestart := s.recordFailure(&Process{}, nil)
	if shouldRestart {
		t.Error("Stop strategy should never restart")
	}
}

func TestSupervisionRecordFailure_MaxRestarts(t *testing.T) {
	gaveUp := false
	s := &Supervision{
		Strategy:    Restart,
		MaxRestarts: 2,
		Window:      time.Minute,
		OnGiveUp:    func(p *Process, err error) { gaveUp = true },
	}

	// First two failures should allow restart
	if !s.recordFailure(&Process{}, nil) {
		t.Error("First failure should allow restart")
	}
	if !s.recordFailure(&Process{}, nil) {
		t.Error("Second failure should allow restart")
	}
	// Third failure exceeds MaxRestarts
	if s.recordFailure(&Process{}, nil) {
		t.Error("Third failure should NOT allow restart (max 2)")
	}
	if !gaveUp {
		t.Error("OnGiveUp should have been called")
	}
}

func TestSupervisionRecordFailure_WindowPruning(t *testing.T) {
	s := &Supervision{
		Strategy:    Restart,
		MaxRestarts: 2,
		Window:      10 * time.Millisecond,
	}

	s.recordFailure(&Process{}, nil)
	s.recordFailure(&Process{}, nil)

	// Wait for window to expire
	time.Sleep(20 * time.Millisecond)

	// After window expires, old failures are pruned
	if !s.recordFailure(&Process{}, nil) {
		t.Error("After window expiry, failures should be pruned and restart allowed")
	}
}

func TestSupervisionRecordFailure_OnFailureCallback(t *testing.T) {
	called := false
	s := &Supervision{
		Strategy:    Restart,
		MaxRestarts: 5,
		OnFailure:   func(p *Process, err error) { called = true },
	}

	s.recordFailure(&Process{}, nil)
	if !called {
		t.Error("OnFailure callback should have been called")
	}
}

func TestSupervisionPrepareRestart(t *testing.T) {
	restartAttempt := 0
	s := &Supervision{
		Strategy:    Restart,
		MaxRestarts: 5,
		OnRestart:   func(p *Process, attempt int) { restartAttempt = attempt },
	}

	s.prepareRestart(&Process{})
	if restartAttempt != 1 {
		t.Errorf("OnRestart attempt = %d, want 1", restartAttempt)
	}

	s.prepareRestart(&Process{})
	if restartAttempt != 2 {
		t.Errorf("OnRestart attempt = %d, want 2", restartAttempt)
	}
}

func TestSupervisionCalculateBackoff_Exponential(t *testing.T) {
	s := &Supervision{
		Backoff: BackoffConfig{
			Initial:    100 * time.Millisecond,
			Multiplier: 2.0,
			Max:        5 * time.Second,
			Type:       BackoffExponential,
		},
	}

	// First restart
	s.mu.Lock()
	s.restarts = 1
	d := s.calculateBackoff()
	s.mu.Unlock()

	if d != 100*time.Millisecond {
		t.Errorf("First backoff = %v, want 100ms", d)
	}

	// Second restart (2x)
	s.mu.Lock()
	s.restarts = 2
	d = s.calculateBackoff()
	s.mu.Unlock()

	if d != 200*time.Millisecond {
		t.Errorf("Second backoff = %v, want 200ms", d)
	}
}

func TestSupervisionCalculateBackoff_Linear(t *testing.T) {
	s := &Supervision{
		Backoff: BackoffConfig{
			Initial: 100 * time.Millisecond,
			Type:    BackoffLinear,
		},
	}

	s.mu.Lock()
	s.restarts = 3
	d := s.calculateBackoff()
	s.mu.Unlock()

	if d != 300*time.Millisecond {
		t.Errorf("Linear backoff at restart 3 = %v, want 300ms", d)
	}
}

func TestSupervisionCalculateBackoff_Constant(t *testing.T) {
	s := &Supervision{
		Backoff: BackoffConfig{
			Initial: 100 * time.Millisecond,
			Type:    BackoffConstant,
		},
	}

	s.mu.Lock()
	s.restarts = 5
	d := s.calculateBackoff()
	s.mu.Unlock()

	if d != 100*time.Millisecond {
		t.Errorf("Constant backoff = %v, want 100ms", d)
	}
}

func TestSupervisionCalculateBackoff_Max(t *testing.T) {
	s := &Supervision{
		Backoff: BackoffConfig{
			Initial:    100 * time.Millisecond,
			Multiplier: 10.0,
			Max:        500 * time.Millisecond,
			Type:       BackoffExponential,
		},
	}

	s.mu.Lock()
	s.restarts = 5
	d := s.calculateBackoff()
	s.mu.Unlock()

	if d > 500*time.Millisecond {
		t.Errorf("Backoff %v should be capped at 500ms", d)
	}
}

func TestSupervisionCalculateBackoff_NoInitial(t *testing.T) {
	s := &Supervision{
		Backoff: BackoffConfig{Type: BackoffExponential},
	}

	s.mu.Lock()
	s.restarts = 5
	d := s.calculateBackoff()
	s.mu.Unlock()

	if d != 0 {
		t.Errorf("Backoff with no initial = %v, want 0", d)
	}
}

func TestSupervisionResetState(t *testing.T) {
	s := &Supervision{
		Strategy:    Restart,
		MaxRestarts: 5,
	}

	s.recordFailure(&Process{}, nil)
	s.recordFailure(&Process{}, nil)
	s.prepareRestart(&Process{})

	s.reset()

	s.mu.Lock()
	if len(s.failures) != 0 {
		t.Error("After reset, failures should be empty")
	}
	if s.restarts != 0 {
		t.Error("After reset, restarts should be 0")
	}
	if s.lastBackoff != 0 {
		t.Error("After reset, lastBackoff should be 0")
	}
	s.mu.Unlock()
}

func TestSupervisionUnlimitedRestarts(t *testing.T) {
	s := &Supervision{
		Strategy:    Restart,
		MaxRestarts: -1, // unlimited
	}

	// Should always allow restart
	for i := 0; i < 100; i++ {
		if !s.recordFailure(&Process{}, nil) {
			t.Fatalf("Unlimited restarts should always return true, failed at iteration %d", i)
		}
	}
}
