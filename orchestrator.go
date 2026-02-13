package vega

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/everydev1618/govega/internal/container"
)

// Orchestrator manages multiple processes.
type Orchestrator struct {
	processes map[string]*Process
	mu        sync.RWMutex

	// Named process registry
	names   map[string]*Process
	namesMu sync.RWMutex

	// Agent registry for respawning
	agents   map[string]Agent
	agentsMu sync.RWMutex

	// Process groups for multi-agent collaboration
	groups   map[string]*ProcessGroup
	groupsMu sync.RWMutex

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
		names:        make(map[string]*Process),
		agents:       make(map[string]Agent),
		groups:       make(map[string]*ProcessGroup),
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

// WithMessages initializes the process with existing conversation history.
// This is useful for resuming conversations or providing context from previous interactions.
func WithMessages(messages []Message) SpawnOption {
	return func(p *Process) {
		p.mu.Lock()
		p.messages = make([]Message, len(messages))
		copy(p.messages, messages)
		p.mu.Unlock()
	}
}

// WithParent sets the parent process for spawn tree tracking.
// This establishes the parent-child relationship for visualization.
func WithParent(parent *Process) SpawnOption {
	return func(p *Process) {
		if parent == nil {
			return
		}
		p.ParentID = parent.ID
		if parent.Agent != nil {
			p.ParentAgent = parent.Agent.Name
		}
		p.SpawnDepth = parent.SpawnDepth + 1

		// Add this process to parent's children list
		parent.childMu.Lock()
		parent.ChildIDs = append(parent.ChildIDs, p.ID)
		parent.childMu.Unlock()
	}
}

// WithSpawnReason sets the reason/task for spawning this process.
// This provides context for why the process was created.
func WithSpawnReason(reason string) SpawnOption {
	return func(p *Process) {
		p.SpawnReason = reason
	}
}

// Spawn creates and starts a new process from an agent.
func (o *Orchestrator) Spawn(agent Agent, opts ...SpawnOption) (*Process, error) {
	// Validate agent
	if agent.Name == "" {
		return nil, &ProcessError{Err: errors.New("agent name is required")}
	}

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

	slog.Info("process spawned",
		"process_id", p.ID,
		"agent", agent.Name,
		"task", p.Task,
	)

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
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}

	slog.Info("process completed",
		"process_id", p.ID,
		"agent", agentName,
		"result_length", len(result),
	)

	o.callbackMu.RLock()
	callbacks := make([]func(*Process, string), len(o.onComplete))
	copy(callbacks, o.onComplete)
	o.callbackMu.RUnlock()

	// Run callbacks synchronously first so they can access the process by name
	var wg sync.WaitGroup
	for _, fn := range callbacks {
		wg.Add(1)
		go func(f func(*Process, string)) {
			defer wg.Done()
			f(p, result)
		}(fn)
	}
	wg.Wait()

	// Unregister name AFTER callbacks complete
	if name := p.Name(); name != "" {
		o.Unregister(name)
	}

	// Leave all groups
	o.LeaveAllGroups(p)
}

// emitFailed notifies all failed callbacks.
func (o *Orchestrator) emitFailed(p *Process, err error) {
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}

	slog.Error("process failed",
		"process_id", p.ID,
		"agent", agentName,
		"error", err.Error(),
	)

	o.callbackMu.RLock()
	callbacks := make([]func(*Process, error), len(o.onFailed))
	copy(callbacks, o.onFailed)
	o.callbackMu.RUnlock()

	// Run callbacks synchronously first so they can access the process by name
	var wg sync.WaitGroup
	for _, fn := range callbacks {
		wg.Add(1)
		go func(f func(*Process, error)) {
			defer wg.Done()
			f(p, err)
		}(fn)
	}
	wg.Wait()

	// Unregister name AFTER callbacks complete
	if name := p.Name(); name != "" {
		o.Unregister(name)
	}

	// Leave all groups
	o.LeaveAllGroups(p)

	// Handle automatic restart if configured
	go o.handleAutoRestart(p, err)
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

// rateLimiter implements token bucket rate limiting.
type rateLimiter struct {
	config   RateLimitConfig
	tokens   float64
	lastTime time.Time
	mu       sync.Mutex
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

// --- Named Process Registry ---

// Register associates a name with a process.
// Returns error if name is already taken.
// The process will be automatically unregistered when it exits.
func (o *Orchestrator) Register(name string, p *Process) error {
	if name == "" {
		return ErrInvalidInput
	}

	o.namesMu.Lock()
	defer o.namesMu.Unlock()

	if existing, ok := o.names[name]; ok && existing != p {
		return &ProcessError{ProcessID: p.ID, AgentName: p.Agent.Name, Err: ErrNameTaken}
	}

	o.names[name] = p
	p.mu.Lock()
	p.name = name
	p.mu.Unlock()

	return nil
}

// Unregister removes a name association.
func (o *Orchestrator) Unregister(name string) {
	o.namesMu.Lock()
	defer o.namesMu.Unlock()

	if p, ok := o.names[name]; ok {
		p.mu.Lock()
		p.name = ""
		p.mu.Unlock()
		delete(o.names, name)
	}
}

// GetByName returns a process by its registered name.
// Returns nil if no process is registered with that name.
func (o *Orchestrator) GetByName(name string) *Process {
	o.namesMu.RLock()
	defer o.namesMu.RUnlock()
	return o.names[name]
}

// RegisterAgent registers an agent definition for later respawning.
// This is required for automatic restart to work.
func (o *Orchestrator) RegisterAgent(agent Agent) {
	o.agentsMu.Lock()
	defer o.agentsMu.Unlock()
	o.agents[agent.Name] = agent
}

// GetAgent returns a registered agent by name.
func (o *Orchestrator) GetAgent(name string) (Agent, bool) {
	o.agentsMu.RLock()
	defer o.agentsMu.RUnlock()
	agent, ok := o.agents[name]
	return agent, ok
}
