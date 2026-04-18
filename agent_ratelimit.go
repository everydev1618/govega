package vega

import (
	"sync"
	"time"
)

// agentRateLimiter implements per-agent token bucket rate limiting.
// When an agent's RateLimit is set, each LLM call must pass through this
// limiter before executing.
type agentRateLimiter struct {
	config   *RateLimit
	tokens   float64
	lastTime time.Time
	mu       sync.Mutex
}

func newAgentRateLimiter(config *RateLimit) *agentRateLimiter {
	if config == nil {
		return &agentRateLimiter{}
	}
	return &agentRateLimiter{
		config:   config,
		tokens:   float64(config.RequestsPerMinute),
		lastTime: time.Now(),
	}
}

// Allow checks if a request is permitted under the rate limit.
// Returns true if the request can proceed.
func (r *agentRateLimiter) Allow() bool {
	if r.config == nil || r.config.RequestsPerMinute <= 0 {
		return true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.refill()

	if r.tokens >= 1 {
		r.tokens--
		return true
	}
	return false
}

// WaitTime returns how long to wait before a token becomes available.
// Returns 0 if a request can proceed immediately.
func (r *agentRateLimiter) WaitTime() time.Duration {
	if r.config == nil || r.config.RequestsPerMinute <= 0 {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.refill()

	if r.tokens >= 1 {
		return 0
	}

	// Time until one token is refilled
	tokensNeeded := 1.0 - r.tokens
	minutesNeeded := tokensNeeded / float64(r.config.RequestsPerMinute)
	return time.Duration(minutesNeeded * float64(time.Minute))
}

func (r *agentRateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(r.lastTime).Minutes()
	r.lastTime = now

	r.tokens += elapsed * float64(r.config.RequestsPerMinute)
	max := float64(r.config.RequestsPerMinute)
	if r.tokens > max {
		r.tokens = max
	}
}

// circuitState represents the state of a circuit breaker.
type circuitState int

const (
	circuitClosed   circuitState = iota // normal operation
	circuitOpen                         // blocking requests
	circuitHalfOpen                     // testing recovery
)

// circuitBreakerState tracks the runtime state of a circuit breaker.
type circuitBreakerState struct {
	config       *CircuitBreaker
	state        circuitState
	failures     int
	halfOpenUsed int
	openedAt     time.Time
	mu           sync.Mutex
}

func newCircuitBreakerState(config *CircuitBreaker) *circuitBreakerState {
	return &circuitBreakerState{
		config: config,
		state:  circuitClosed,
	}
}

// Allow checks if a request is permitted through the circuit breaker.
func (cb *circuitBreakerState) Allow() bool {
	if cb.config == nil {
		return true
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		return true

	case circuitOpen:
		if time.Since(cb.openedAt) >= cb.config.ResetAfter {
			cb.state = circuitHalfOpen
			cb.halfOpenUsed = 0
			return cb.tryHalfOpen()
		}
		return false

	case circuitHalfOpen:
		return cb.tryHalfOpen()
	}
	return false
}

func (cb *circuitBreakerState) tryHalfOpen() bool {
	max := cb.config.HalfOpenMax
	if max <= 0 {
		max = 1
	}
	if cb.halfOpenUsed < max {
		cb.halfOpenUsed++
		return true
	}
	return false
}

// RecordSuccess records a successful request.
func (cb *circuitBreakerState) RecordSuccess() {
	if cb.config == nil {
		return
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	wasBroken := cb.state != circuitClosed
	cb.state = circuitClosed
	cb.failures = 0
	cb.halfOpenUsed = 0

	if wasBroken && cb.config.OnClose != nil {
		cb.config.OnClose()
	}
}

// RecordFailure records a failed request.
func (cb *circuitBreakerState) RecordFailure() {
	if cb.config == nil {
		return
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++

	switch cb.state {
	case circuitClosed:
		if cb.config.Threshold > 0 && cb.failures >= cb.config.Threshold {
			cb.state = circuitOpen
			cb.openedAt = time.Now()
			if cb.config.OnOpen != nil {
				cb.config.OnOpen()
			}
		}

	case circuitHalfOpen:
		// Failed during probe — reopen
		cb.state = circuitOpen
		cb.openedAt = time.Now()
	}
}
