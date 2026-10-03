package vega

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// Supervision configures fault tolerance for a process.
type Supervision struct {
	// Strategy determines what happens when a process fails
	Strategy Strategy

	// MaxRestarts is the maximum restart count within Window (-1 for unlimited)
	MaxRestarts int

	// Window is the time window for counting restarts
	Window time.Duration

	// Backoff configures delay between restarts
	Backoff BackoffConfig

	// OnFailure is called when the process fails
	OnFailure func(p *Process, err error)

	// OnRestart is called before restarting
	OnRestart func(p *Process, attempt int)

	// OnGiveUp is called when max restarts exceeded
	OnGiveUp func(p *Process, err error)

	// state holds the mutex-guarded restart bookkeeping. It lives behind a
	// pointer so that Supervision stays copyable: callers write config as a
	// literal and pass it by value (WithSupervision), and a struct holding a
	// sync.Mutex directly cannot be copied without tripping go vet. It is
	// created on first use, so a zero-value Supervision is usable.
	state *supervisionState
}

// supervisionState is the mutable half of a Supervision.
type supervisionState struct {
	mu          sync.Mutex
	failures    []time.Time
	restarts    int
	lastBackoff time.Duration
}

// supervisionInit guards lazy creation of supervisionState. A package-level
// lock keeps Supervision itself free of any non-copyable field; it is only
// contended when supervision state is first touched, which is rare.
var supervisionInit sync.Mutex

// st returns the supervision state, creating it on first use.
func (s *Supervision) st() *supervisionState {
	supervisionInit.Lock()
	defer supervisionInit.Unlock()
	if s.state == nil {
		s.state = &supervisionState{}
	}
	return s.state
}

// failureCount reports how many failures are currently inside the window.
func (s *Supervision) failureCount() int {
	st := s.st()
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.failures)
}

// restartCount reports how many restarts have been prepared.
func (s *Supervision) restartCount() int {
	st := s.st()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.restarts
}

// Strategy determines restart behavior.
type Strategy int

const (
	// Restart restarts the failed process
	Restart Strategy = iota

	// Stop lets the process stay dead
	Stop

	// Escalate propagates failure to parent
	Escalate

	// RestartAll restarts all sibling processes (for interdependent processes)
	RestartAll
)

// String returns the strategy name.
func (s Strategy) String() string {
	switch s {
	case Restart:
		return "restart"
	case Stop:
		return "stop"
	case Escalate:
		return "escalate"
	case RestartAll:
		return "restart_all"
	default:
		return "unknown"
	}
}

// recordFailure records a failure and returns whether restart should happen.
func (s *Supervision) recordFailure(p *Process, err error) bool {
	st := s.st()
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.failures = append(st.failures, now)

	// Prune old failures outside the window
	if s.Window > 0 {
		cutoff := now.Add(-s.Window)
		newFailures := make([]time.Time, 0, len(st.failures))
		for _, t := range st.failures {
			if t.After(cutoff) {
				newFailures = append(newFailures, t)
			}
		}
		st.failures = newFailures
	}

	// Call failure callback
	if s.OnFailure != nil {
		s.OnFailure(p, err)
	}

	// Check if we should restart
	if s.Strategy == Stop {
		return false
	}

	if s.MaxRestarts >= 0 && len(st.failures) > s.MaxRestarts {
		// Exceeded max restarts
		if s.OnGiveUp != nil {
			s.OnGiveUp(p, err)
		}
		return false
	}

	return true
}

// prepareRestart prepares for a restart and returns the backoff delay.
func (s *Supervision) prepareRestart(p *Process) time.Duration {
	st := s.st()
	st.mu.Lock()
	defer st.mu.Unlock()

	st.restarts++

	// Call restart callback
	if s.OnRestart != nil {
		s.OnRestart(p, st.restarts)
	}

	// Calculate backoff
	delay := s.calculateBackoff(st.restarts)
	st.lastBackoff = delay
	return delay
}

// calculateBackoff returns the delay before next restart.
func (s *Supervision) calculateBackoff(restarts int) time.Duration {
	if s.Backoff.Initial == 0 {
		return 0
	}

	var delay time.Duration

	switch s.Backoff.Type {
	case BackoffExponential:
		multiplier := s.Backoff.Multiplier
		if multiplier == 0 {
			multiplier = 2.0
		}
		delay = time.Duration(float64(s.Backoff.Initial) * math.Pow(multiplier, float64(restarts-1)))

	case BackoffLinear:
		delay = s.Backoff.Initial * time.Duration(restarts)

	case BackoffConstant:
		delay = s.Backoff.Initial
	}

	// Apply max
	if s.Backoff.Max > 0 && delay > s.Backoff.Max {
		delay = s.Backoff.Max
	}

	// Apply jitter
	if s.Backoff.Jitter > 0 {
		jitter := float64(delay) * s.Backoff.Jitter * (rand.Float64()*2 - 1)
		delay = time.Duration(float64(delay) + jitter)
	}

	return delay
}

// reset resets the supervision state.
func (s *Supervision) reset() {
	st := s.st()
	st.mu.Lock()
	defer st.mu.Unlock()

	st.failures = nil
	st.restarts = 0
	st.lastBackoff = 0
}

// HealthMonitor monitors process health.
type HealthMonitor struct {
	config   HealthConfig
	alertCh  chan Alert
	stopCh   chan struct{}
	mu       sync.Mutex
	monitors map[string]*processMonitor
}

// HealthConfig configures health monitoring.
type HealthConfig struct {
	// CheckInterval is how often to check health
	CheckInterval time.Duration

	// StaleProgressMinutes warns if no progress
	StaleProgressMinutes int

	// MaxIterationsWarning warns if iterations exceed this
	MaxIterationsWarning int

	// ErrorLoopCount is consecutive errors before alert
	ErrorLoopCount int

	// CostAlertUSD alerts when cost exceeds this
	CostAlertUSD float64
}

// processMonitor tracks health for a single process.
type processMonitor struct {
	lastProgress  time.Time
	lastIteration int
	errorCount    int
	lastCostAlert float64
}

// Alert represents a health alert.
type Alert struct {
	ProcessID string
	AgentName string
	Type      AlertType
	Message   string
	Timestamp time.Time
}

// AlertType categorizes alerts.
type AlertType string

const (
	AlertStaleProgress  AlertType = "stale_progress"
	AlertHighCost       AlertType = "high_cost"
	AlertErrorLoop      AlertType = "error_loop"
	AlertTimeoutWarning AlertType = "timeout_warning"
	AlertHighIterations AlertType = "high_iterations"
)

// NewHealthMonitor creates a new health monitor.
func NewHealthMonitor(config HealthConfig) *HealthMonitor {
	return &HealthMonitor{
		config:   config,
		alertCh:  make(chan Alert, 100),
		stopCh:   make(chan struct{}),
		monitors: make(map[string]*processMonitor),
	}
}

// Alerts returns the channel of health alerts.
func (h *HealthMonitor) Alerts() <-chan Alert {
	return h.alertCh
}

// Start begins health monitoring.
func (h *HealthMonitor) Start(getProcesses func() []*Process) {
	if h.config.CheckInterval == 0 {
		h.config.CheckInterval = 30 * time.Second
	}

	go func() {
		ticker := time.NewTicker(h.config.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-h.stopCh:
				return
			case <-ticker.C:
				h.checkHealth(getProcesses())
			}
		}
	}()
}

// Stop stops health monitoring.
func (h *HealthMonitor) Stop() {
	close(h.stopCh)
}

// checkHealth checks all process health.
func (h *HealthMonitor) checkHealth(processes []*Process) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()

	for _, p := range processes {
		if p.Status() != StatusRunning {
			continue
		}

		monitor, ok := h.monitors[p.ID]
		if !ok {
			monitor = &processMonitor{
				lastProgress:  now,
				lastIteration: p.iteration,
			}
			h.monitors[p.ID] = monitor
		}

		metrics := p.Metrics()

		// Check for stale progress
		if h.config.StaleProgressMinutes > 0 {
			if metrics.Iterations > monitor.lastIteration {
				monitor.lastProgress = now
				monitor.lastIteration = metrics.Iterations
			} else {
				staleMinutes := int(now.Sub(monitor.lastProgress).Minutes())
				if staleMinutes >= h.config.StaleProgressMinutes {
					h.sendAlert(Alert{
						ProcessID: p.ID,
						AgentName: p.Agent.Name,
						Type:      AlertStaleProgress,
						Message:   "No progress for " + string(rune(staleMinutes)) + " minutes",
						Timestamp: now,
					})
				}
			}
		}

		// Check for high iterations
		if h.config.MaxIterationsWarning > 0 && metrics.Iterations >= h.config.MaxIterationsWarning {
			h.sendAlert(Alert{
				ProcessID: p.ID,
				AgentName: p.Agent.Name,
				Type:      AlertHighIterations,
				Message:   "Iteration count exceeded warning threshold",
				Timestamp: now,
			})
		}

		// Check for high cost
		if h.config.CostAlertUSD > 0 && metrics.CostUSD >= h.config.CostAlertUSD {
			if metrics.CostUSD > monitor.lastCostAlert {
				monitor.lastCostAlert = metrics.CostUSD
				h.sendAlert(Alert{
					ProcessID: p.ID,
					AgentName: p.Agent.Name,
					Type:      AlertHighCost,
					Message:   "Cost exceeded threshold",
					Timestamp: now,
				})
			}
		}

		// Check for error loop
		if h.config.ErrorLoopCount > 0 && metrics.Errors >= h.config.ErrorLoopCount {
			h.sendAlert(Alert{
				ProcessID: p.ID,
				AgentName: p.Agent.Name,
				Type:      AlertErrorLoop,
				Message:   "Multiple consecutive errors",
				Timestamp: now,
			})
		}
	}

	// Clean up monitors for dead processes
	activeIDs := make(map[string]bool)
	for _, p := range processes {
		activeIDs[p.ID] = true
	}
	for id := range h.monitors {
		if !activeIDs[id] {
			delete(h.monitors, id)
		}
	}
}

// sendAlert sends an alert if channel has capacity.
func (h *HealthMonitor) sendAlert(alert Alert) {
	select {
	case h.alertCh <- alert:
	default:
		// Channel full, drop alert
	}
}
