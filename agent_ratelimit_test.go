package vega

import (
	"testing"
	"time"
)

// --- Agent Rate Limiter ---

func TestAgentRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := newAgentRateLimiter(&RateLimit{RequestsPerMinute: 10})
	for i := 0; i < 10; i++ {
		if !rl.Allow() {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestAgentRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := newAgentRateLimiter(&RateLimit{RequestsPerMinute: 2})
	rl.Allow()
	rl.Allow()
	if rl.Allow() {
		t.Fatal("third request should be blocked")
	}
}

func TestAgentRateLimiter_RefillsOverTime(t *testing.T) {
	rl := newAgentRateLimiter(&RateLimit{RequestsPerMinute: 60})
	// Drain all tokens
	for i := 0; i < 60; i++ {
		rl.Allow()
	}
	if rl.Allow() {
		t.Fatal("should be empty")
	}
	// Simulate time passing (1 second = 1 token for 60 RPM)
	rl.mu.Lock()
	rl.lastTime = rl.lastTime.Add(-2 * time.Second)
	rl.mu.Unlock()

	if !rl.Allow() {
		t.Fatal("should have refilled after time passed")
	}
}

func TestAgentRateLimiter_WaitReturnsDelay(t *testing.T) {
	rl := newAgentRateLimiter(&RateLimit{RequestsPerMinute: 60})
	// Drain
	for i := 0; i < 60; i++ {
		rl.Allow()
	}
	delay := rl.WaitTime()
	if delay <= 0 {
		t.Fatal("should return positive delay when exhausted")
	}
	if delay > 2*time.Second {
		t.Fatalf("delay %v too large for 60 RPM", delay)
	}
}

func TestAgentRateLimiter_NilConfigNeverBlocks(t *testing.T) {
	rl := newAgentRateLimiter(nil)
	for i := 0; i < 1000; i++ {
		if !rl.Allow() {
			t.Fatal("nil config should never block")
		}
	}
}

// --- Circuit Breaker ---

func TestCircuitBreaker_StartsClosedAllowsRequests(t *testing.T) {
	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:   3,
		ResetAfter:  10 * time.Second,
		HalfOpenMax: 1,
	})
	if !cb.Allow() {
		t.Fatal("closed breaker should allow requests")
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:   3,
		ResetAfter:  10 * time.Second,
		HalfOpenMax: 1,
	})

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.Allow() {
		t.Fatal("should be open after 3 failures")
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:   2,
		ResetAfter:  100 * time.Millisecond,
		HalfOpenMax: 1,
	})

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("should be open")
	}

	// Wait for reset
	time.Sleep(150 * time.Millisecond)

	if !cb.Allow() {
		t.Fatal("should be half-open and allow one request")
	}
	// Second request in half-open should be blocked
	if cb.Allow() {
		t.Fatal("half-open should only allow HalfOpenMax requests")
	}
}

func TestCircuitBreaker_ClosesOnSuccess(t *testing.T) {
	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:   2,
		ResetAfter:  100 * time.Millisecond,
		HalfOpenMax: 1,
	})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(150 * time.Millisecond)

	cb.Allow() // half-open probe
	cb.RecordSuccess()

	// Should be closed now, allow many requests
	for i := 0; i < 10; i++ {
		if !cb.Allow() {
			t.Fatalf("request %d should be allowed after circuit closed", i)
		}
	}
}

func TestCircuitBreaker_ReopensOnHalfOpenFailure(t *testing.T) {
	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:   2,
		ResetAfter:  100 * time.Millisecond,
		HalfOpenMax: 1,
	})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(150 * time.Millisecond)

	cb.Allow() // half-open probe
	cb.RecordFailure()

	if cb.Allow() {
		t.Fatal("should be open again after half-open failure")
	}
}

func TestCircuitBreaker_CallbacksInvoked(t *testing.T) {
	openCalled := false
	closeCalled := false

	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:   1,
		ResetAfter:  100 * time.Millisecond,
		HalfOpenMax: 1,
		OnOpen:      func() { openCalled = true },
		OnClose:     func() { closeCalled = true },
	})

	cb.RecordFailure()
	if !openCalled {
		t.Fatal("OnOpen should have been called")
	}

	time.Sleep(150 * time.Millisecond)
	cb.Allow()
	cb.RecordSuccess()
	if !closeCalled {
		t.Fatal("OnClose should have been called")
	}
}

func TestCircuitBreaker_NilConfigNeverBlocks(t *testing.T) {
	cb := newCircuitBreakerState(nil)
	for i := 0; i < 100; i++ {
		cb.RecordFailure()
	}
	if !cb.Allow() {
		t.Fatal("nil config should never block")
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := newCircuitBreakerState(&CircuitBreaker{
		Threshold:  3,
		ResetAfter: 10 * time.Second,
	})

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // reset

	cb.RecordFailure()
	cb.RecordFailure()
	// Only 2 failures since last success, should still be closed
	if !cb.Allow() {
		t.Fatal("should still be closed after success reset")
	}
}
