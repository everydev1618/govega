package vega

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/martellcode/vega/container"
)

// Orchestrator manages multiple processes.
type Orchestrator struct {
	processes map[string]*Process
	mu        sync.RWMutex

	// Configuration
	maxProcesses  int
	defaultLLM    LLM
	persistence   Persistence
	healthMonitor *HealthMonitor
	recovery      bool

	// Rate limiting
	rateLimits map[string]*rateLimiter

	// Container management
	containerManager  *container.Manager
	containerRegistry *container.ProjectRegistry

	// Lifecycle callbacks
	onComplete []func(*Process, string)
	onFailed   []func(*Process, error)
	onStarted  []func(*Process)
	callbackMu sync.RWMutex

	// Event callbacks (for distributed workers)
	callbackConfig *CallbackConfig
	eventPoller    *EventPoller

	// Shutdown coordination
	ctx    context.Context
	cancel context.CancelFunc
}

// ProcessEvent represents a process lifecycle event.
type ProcessEvent struct {
	Type      ProcessEventType
	Process   *Process
	Result    string // For complete events
	Error     error  // For failed events
	Timestamp time.Time
}

// ProcessEventType is the type of lifecycle event.
type ProcessEventType int

const (
	ProcessStarted ProcessEventType = iota
	ProcessCompleted
	ProcessFailed
)

// OrchestratorOption configures an Orchestrator.
type OrchestratorOption func(*Orchestrator)

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(opts ...OrchestratorOption) *Orchestrator {
	ctx, cancel := context.WithCancel(context.Background())

	o := &Orchestrator{
		processes:    make(map[string]*Process),
		maxProcesses: 100,
		rateLimits:   make(map[string]*rateLimiter),
		ctx:          ctx,
		cancel:       cancel,
	}

	for _, opt := range opts {
		opt(o)
	}

	// Start health monitoring if configured
	if o.healthMonitor != nil {
		o.healthMonitor.Start(o.List)
	}

	// Recover processes if enabled
	if o.recovery && o.persistence != nil {
		o.recoverProcesses()
	}

	return o
}

// WithMaxProcesses sets the maximum number of concurrent processes.
func WithMaxProcesses(n int) OrchestratorOption {
	return func(o *Orchestrator) {
		o.maxProcesses = n
	}
}

// WithLLM sets the default LLM backend.
func WithLLM(llm LLM) OrchestratorOption {
	return func(o *Orchestrator) {
		o.defaultLLM = llm
	}
}

// WithPersistence enables process state persistence.
func WithPersistence(p Persistence) OrchestratorOption {
	return func(o *Orchestrator) {
		o.persistence = p
	}
}

// WithRecovery enables process recovery on startup.
func WithRecovery(enabled bool) OrchestratorOption {
	return func(o *Orchestrator) {
		o.recovery = enabled
	}
}

// WithHealthCheck enables health monitoring.
func WithHealthCheck(config HealthConfig) OrchestratorOption {
	return func(o *Orchestrator) {
		o.healthMonitor = NewHealthMonitor(config)
	}
}

// WithRateLimits configures per-model rate limiting.
func WithRateLimits(limits map[string]RateLimitConfig) OrchestratorOption {
	return func(o *Orchestrator) {
		for model, config := range limits {
			o.rateLimits[model] = newRateLimiter(config)
		}
	}
}

// WithContainerManager enables container-based project isolation.
// If baseDir is provided, a ProjectRegistry will also be created.
func WithContainerManager(cm *container.Manager, baseDir string) OrchestratorOption {
	return func(o *Orchestrator) {
		o.containerManager = cm
		if baseDir != "" && cm != nil {
			registry, err := container.NewProjectRegistry(baseDir, cm)
			if err == nil {
				o.containerRegistry = registry
			}
		}
	}
}

// RateLimitConfig configures rate limiting for a model.
type RateLimitConfig struct {
	RequestsPerMinute int
	TokensPerMinute   int
	Strategy          RateLimitStrategy
}

// RateLimitStrategy determines rate limit behavior.
type RateLimitStrategy int

const (
	RateLimitQueue RateLimitStrategy = iota
	RateLimitReject
	RateLimitBackpressure
)

// SpawnOption configures a spawned process.
type SpawnOption func(*Process)

// WithTask sets the task description.
func WithTask(task string) SpawnOption {
	return func(p *Process) {
		p.Task = task
	}
}

// WithWorkDir sets the working directory.
func WithWorkDir(dir string) SpawnOption {
	return func(p *Process) {
		p.WorkDir = dir
	}
}

// WithSupervision sets the supervision configuration.
func WithSupervision(s Supervision) SpawnOption {
	return func(p *Process) {
		p.Supervision = &s
	}
}

// WithTimeout sets a timeout for the process.
func WithTimeout(d time.Duration) SpawnOption {
	return func(p *Process) {
		ctx, cancel := context.WithTimeout(p.ctx, d)
		p.ctx = ctx
		oldCancel := p.cancel
		p.cancel = func() {
			cancel()
			if oldCancel != nil {
				oldCancel()
			}
		}
	}
}

// WithMaxIterations sets the maximum iteration count.
func WithMaxIterations(n int) SpawnOption {
	return func(p *Process) {
		// Store in process for checking
		// This is checked in the LLM loop
	}
}

// WithProcessContext sets a parent context.
func WithProcessContext(ctx context.Context) SpawnOption {
	return func(p *Process) {
		p.ctx, p.cancel = context.WithCancel(ctx)
	}
}

// WithProject sets the container project for isolated execution.
func WithProject(name string) SpawnOption {
	return func(p *Process) {
		p.Project = name
	}
}

// Spawn creates and starts a new process from an agent.
func (o *Orchestrator) Spawn(agent Agent, opts ...SpawnOption) (*Process, error) {
	o.mu.Lock()

	// Check capacity
	if len(o.processes) >= o.maxProcesses {
		o.mu.Unlock()
		return nil, ErrMaxProcessesReached
	}

	// Create process
	ctx, cancel := context.WithCancel(o.ctx)
	p := &Process{
		ID:           uuid.New().String()[:8],
		Agent:        &agent,
		status:       StatusPending,
		StartedAt:    time.Now(),
		ctx:          ctx,
		cancel:       cancel,
		orchestrator: o,
		messages:     make([]Message, 0),
		metrics: ProcessMetrics{
			StartedAt: time.Now(),
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(p)
	}

	// Set LLM backend
	if agent.LLM != nil {
		p.llm = agent.LLM
	} else if o.defaultLLM != nil {
		p.llm = o.defaultLLM
	} else {
		o.mu.Unlock()
		return nil, &ProcessError{ProcessID: p.ID, AgentName: agent.Name, Err: ErrProcessNotRunning}
	}

	// Register process
	o.processes[p.ID] = p
	o.mu.Unlock()

	// Persist state
	o.persistState()

	// Mark as running
	p.mu.Lock()
	p.status = StatusRunning
	p.mu.Unlock()

	// Emit started event
	o.emitStarted(p)

	return p, nil
}

// Get returns a process by ID.
func (o *Orchestrator) Get(id string) *Process {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.processes[id]
}

// List returns all processes.
func (o *Orchestrator) List() []*Process {
	o.mu.RLock()
	defer o.mu.RUnlock()

	procs := make([]*Process, 0, len(o.processes))
	for _, p := range o.processes {
		procs = append(procs, p)
	}
	return procs
}

// Kill terminates a process.
func (o *Orchestrator) Kill(id string) error {
	o.mu.Lock()
	p, ok := o.processes[id]
	if !ok {
		o.mu.Unlock()
		return ErrProcessNotFound
	}
	o.mu.Unlock()

	p.Stop()

	o.mu.Lock()
	delete(o.processes, id)
	o.mu.Unlock()

	o.persistState()
	return nil
}

// Shutdown gracefully shuts down all processes.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	// Stop health monitor
	if o.healthMonitor != nil {
		o.healthMonitor.Stop()
	}

	// Stop event poller
	if o.eventPoller != nil {
		o.eventPoller.Stop()
	}

	// Close container manager
	if o.containerManager != nil {
		o.containerManager.Close()
	}

	// Cancel all processes
	o.cancel()

	// Wait for processes to stop or context to expire
	done := make(chan struct{})
	go func() {
		o.mu.RLock()
		for _, p := range o.processes {
			p.Stop()
		}
		o.mu.RUnlock()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetContainerManager returns the container manager, if configured.
func (o *Orchestrator) GetContainerManager() *container.Manager {
	return o.containerManager
}

// GetProjectRegistry returns the project registry, if configured.
func (o *Orchestrator) GetProjectRegistry() *container.ProjectRegistry {
	return o.containerRegistry
}

// OnHealthAlert registers a callback for health alerts.
func (o *Orchestrator) OnHealthAlert(fn func(Alert)) {
	if o.healthMonitor == nil {
		return
	}

	go func() {
		for alert := range o.healthMonitor.Alerts() {
			fn(alert)
		}
	}()
}

// OnProcessComplete registers a callback for when a process completes successfully.
// The callback receives the process and its final result.
func (o *Orchestrator) OnProcessComplete(fn func(*Process, string)) {
	o.callbackMu.Lock()
	defer o.callbackMu.Unlock()
	o.onComplete = append(o.onComplete, fn)
}

// OnProcessFailed registers a callback for when a process fails.
// The callback receives the process and the error.
func (o *Orchestrator) OnProcessFailed(fn func(*Process, error)) {
	o.callbackMu.Lock()
	defer o.callbackMu.Unlock()
	o.onFailed = append(o.onFailed, fn)
}

// OnProcessStarted registers a callback for when a process starts.
func (o *Orchestrator) OnProcessStarted(fn func(*Process)) {
	o.callbackMu.Lock()
	defer o.callbackMu.Unlock()
	o.onStarted = append(o.onStarted, fn)
}

// emitComplete notifies all complete callbacks.
func (o *Orchestrator) emitComplete(p *Process, result string) {
	o.callbackMu.RLock()
	callbacks := make([]func(*Process, string), len(o.onComplete))
	copy(callbacks, o.onComplete)
	o.callbackMu.RUnlock()

	for _, fn := range callbacks {
		go fn(p, result)
	}
}

// emitFailed notifies all failed callbacks.
func (o *Orchestrator) emitFailed(p *Process, err error) {
	o.callbackMu.RLock()
	callbacks := make([]func(*Process, error), len(o.onFailed))
	copy(callbacks, o.onFailed)
	o.callbackMu.RUnlock()

	for _, fn := range callbacks {
		go fn(p, err)
	}
}

// emitStarted notifies all started callbacks.
func (o *Orchestrator) emitStarted(p *Process) {
	o.callbackMu.RLock()
	callbacks := make([]func(*Process), len(o.onStarted))
	copy(callbacks, o.onStarted)
	o.callbackMu.RUnlock()

	for _, fn := range callbacks {
		go fn(p)
	}
}

// persistState saves process state.
func (o *Orchestrator) persistState() {
	if o.persistence == nil {
		return
	}

	o.mu.RLock()
	states := make([]ProcessState, 0, len(o.processes))
	for _, p := range o.processes {
		states = append(states, ProcessState{
			ID:          p.ID,
			AgentName:   p.Agent.Name,
			Task:        p.Task,
			WorkDir:     p.WorkDir,
			Status:      p.status,
			StartedAt:   p.StartedAt,
			Metrics:     p.metrics,
		})
	}
	o.mu.RUnlock()

	o.persistence.Save(states)
}

// recoverProcesses recovers processes from persistence.
func (o *Orchestrator) recoverProcesses() {
	if o.persistence == nil {
		return
	}

	states, err := o.persistence.Load()
	if err != nil {
		return
	}

	for _, state := range states {
		if state.Status == StatusRunning || state.Status == StatusPending {
			// Mark as needing restart
			// In a real implementation, we'd need agent definitions to respawn
		}
	}
}

// Persistence interface for saving process state.
type Persistence interface {
	Save(states []ProcessState) error
	Load() ([]ProcessState, error)
}

// ProcessState is the persisted state of a process.
type ProcessState struct {
	ID          string         `json:"id"`
	AgentName   string         `json:"agent_name"`
	Task        string         `json:"task"`
	WorkDir     string         `json:"work_dir"`
	Status      Status         `json:"status"`
	StartedAt   time.Time      `json:"started_at"`
	Metrics     ProcessMetrics `json:"metrics"`
}

// JSONPersistence saves state to a JSON file.
type JSONPersistence struct {
	path string
	mu   sync.Mutex
}

// NewJSONPersistence creates a new JSON file persistence.
func NewJSONPersistence(path string) *JSONPersistence {
	return &JSONPersistence{path: path}
}

// Save writes state to the file.
func (p *JSONPersistence) Save(states []ProcessState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(p.path, data, 0644)
}

// Load reads state from the file.
func (p *JSONPersistence) Load() ([]ProcessState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var states []ProcessState
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, err
	}

	return states, nil
}

// rateLimiter implements token bucket rate limiting.
type rateLimiter struct {
	config    RateLimitConfig
	tokens    float64
	lastTime  time.Time
	mu        sync.Mutex
}

func newRateLimiter(config RateLimitConfig) *rateLimiter {
	return &rateLimiter{
		config:   config,
		tokens:   float64(config.RequestsPerMinute),
		lastTime: time.Now(),
	}
}

func (r *rateLimiter) allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.lastTime).Minutes()
	r.lastTime = now

	// Refill tokens
	r.tokens += elapsed * float64(r.config.RequestsPerMinute)
	if r.tokens > float64(r.config.RequestsPerMinute) {
		r.tokens = float64(r.config.RequestsPerMinute)
	}

	// Check if we have tokens
	if r.tokens >= 1 {
		r.tokens--
		return true
	}

	return false
}
