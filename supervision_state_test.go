package vega

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Supervision carries public config a caller writes as a literal, plus
// mutex-guarded restart bookkeeping. Keeping that bookkeeping behind a pointer
// is what lets WithSupervision keep taking a value without copying a lock.

func TestSupervisionConfigCopiesGetIndependentState(t *testing.T) {
	// A config literal is a template. Copies of it, before any use, must not
	// share restart counters — otherwise two processes spawned from one config
	// would exhaust each other's restart budget.
	cfg := Supervision{Strategy: Restart, MaxRestarts: 2, Window: time.Minute}
	a, b := cfg, cfg

	p := &Process{}
	boom := errors.New("boom")

	for i := range 3 {
		a.recordFailure(p, boom)
		if i == 2 {
			break
		}
	}
	// a has now burned its budget (3 failures > MaxRestarts 2).
	if a.recordFailure(p, boom) {
		t.Fatal("a should have exceeded MaxRestarts")
	}

	// b is a separate template copy and must start clean.
	if !b.recordFailure(p, boom) {
		t.Error("b shares state with a; config copies must be independent")
	}
}

func TestSupervisionSharesStateOnceInUse(t *testing.T) {
	// Once a Supervision is live, copying it must not silently fork the
	// counters — both views report the same bookkeeping.
	s := &Supervision{Strategy: Restart, MaxRestarts: 5, Window: time.Minute}
	p := &Process{}
	s.recordFailure(p, errors.New("first"))

	copied := *s
	if copied.failureCount() != s.failureCount() {
		t.Fatalf("copy sees %d failures, original sees %d",
			copied.failureCount(), s.failureCount())
	}

	copied.recordFailure(p, errors.New("second"))
	if s.failureCount() != 2 {
		t.Errorf("original sees %d failures after copy recorded one, want 2",
			s.failureCount())
	}
}

func TestSupervisionConcurrentRecordFailure(t *testing.T) {
	// Run under -race: the state must be safe for concurrent supervisors.
	s := &Supervision{Strategy: Restart, MaxRestarts: -1, Window: time.Minute}
	p := &Process{}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.recordFailure(p, errors.New("concurrent"))
		}()
	}
	wg.Wait()

	if got := s.failureCount(); got != 50 {
		t.Errorf("failureCount = %d, want 50", got)
	}
}

func TestSupervisionResetClearsState(t *testing.T) {
	s := &Supervision{Strategy: Restart, MaxRestarts: 3, Window: time.Minute}
	p := &Process{}
	s.recordFailure(p, errors.New("x"))
	s.prepareRestart(p)

	s.reset()

	if got := s.failureCount(); got != 0 {
		t.Errorf("failureCount after reset = %d, want 0", got)
	}
	if got := s.restartCount(); got != 0 {
		t.Errorf("restartCount after reset = %d, want 0", got)
	}
}

func TestSupervisionZeroValueIsUsable(t *testing.T) {
	// A bare Supervision{} must work without explicit initialization.
	var s Supervision
	s.Strategy = Restart
	s.MaxRestarts = 1
	if !s.recordFailure(&Process{}, errors.New("x")) {
		t.Error("zero-value Supervision should allow a first restart")
	}
}
