package vega

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/everydev1618/govega/internal/container"
	"github.com/everydev1618/govega/llm"
	"github.com/google/uuid"
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
	defaultLLM    llm.LLM
	persistence   Persistence
	healthMonitor *HealthMonitor
	recovery      bool

	// Terminal-process retention. Completed/failed processes are kept in the
	// registry for short-lived introspection, but only up to maxTerminalRetained;
	// the oldest are evicted FIFO so the registry never grows without bound.
	// terminalOrder is guarded by mu (same lock as processes).
	maxTerminalRetained int
	terminalOrder       []string

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
		processes:           make(map[string]*Process),
		names:               make(map[string]*Process),
		agents:              make(map[string]Agent),
		groups:              make(map[string]*ProcessGroup),
		maxProcesses:        100,
		maxTerminalRetained: 256,
		rateLimits:          make(map[string]*rateLimiter),
		ctx:                 ctx,
		cancel:              cancel,
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

// WithMaxRetainedTerminal sets how many completed/failed processes the registry
// keeps for introspection before evicting the oldest. A value <= 0 disables
// retention (terminal processes are removed as soon as they finish).
func WithMaxRetainedTerminal(n int) OrchestratorOption {
	return func(o *Orchestrator) {
		o.maxTerminalRetained = n
	}
}

// WithLLM sets the default LLM backend.
func WithLLM(l llm.LLM) OrchestratorOption {
	return func(o *Orchestrator) {
		o.defaultLLM = l
	}
}

// WithPersistence enables process state persistence.
func WithPersistence(p Persistence) OrchestratorOption {
	return func(o *Orchestrator) {
		o.persistence = p
	}
}

// WithRecovery enables loading persisted process state on startup.
//
// NOTE: recovery currently loads persisted state and logs what it finds but
// does not yet respawn in-flight processes — durable restart requires the
// agent registry to be populated before recovery runs, which is planned
// durability work. Until then this option is effectively diagnostic; do not
// rely on it to resume interrupted work.
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

// WithMaxIterations overrides the agent's per-turn tool-loop iteration cap
// for this process. Values <= 0 are ignored (the agent default applies).
func WithMaxIterations(n int) SpawnOption {
	return func(p *Process) {
		if n > 0 {
			p.maxIterations = n
		}
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
func WithMessages(messages []llm.Message) SpawnOption {
	return func(p *Process) {
		p.mu.Lock()
		p.messages = make([]llm.Message, len(messages))
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

	// Check capacity — only live (non-terminal) processes count against the cap.
	// Retained terminal processes must not starve new spawns.
	if o.activeCountLocked() >= o.maxProcesses {
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
		messages:     make([]llm.Message, 0),
		metrics: ProcessMetrics{
			StartedAt: time.Now(),
		},
	}

	// Initialize rate limiter and circuit breaker from agent config
	p.rateLimiter = newAgentRateLimiter(agent.RateLimit)
	p.circuitBreaker = newCircuitBreakerState(agent.CircuitBreaker)

	// Apply options
	for _, opt := range opts {
		opt(p)
	}

	// Default WorkDir to shared workspace if not set by options.
	if p.WorkDir == "" {
		p.WorkDir = WorkspacePath()
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

// activeCountLocked returns the number of non-terminal processes.
// Caller must hold o.mu.
func (o *Orchestrator) activeCountLocked() int {
	n := 0
	for _, p := range o.processes {
		if !p.isTerminal() {
			n++
		}
	}
	return n
}

// retireProcess records a now-terminal process for bounded retention and evicts
// the oldest terminal processes once the retention limit is exceeded. This is the
// mechanism that frees completed/failed processes from the registry so it never
// grows without bound. Safe to call more than once for the same process.
func (o *Orchestrator) retireProcess(p *Process) {
	if p == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	// With retention disabled, drop the process immediately.
	if o.maxTerminalRetained <= 0 {
		delete(o.processes, p.ID)
		return
	}

	o.terminalOrder = append(o.terminalOrder, p.ID)
	for len(o.terminalOrder) > o.maxTerminalRetained {
		oldest := o.terminalOrder[0]
		o.terminalOrder = o.terminalOrder[1:]
		delete(o.processes, oldest) // no-op if already removed (e.g. by Kill)
	}
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

	// Wait for processes to stop or context to expire.
	// Snapshot the process list first and release the lock before stopping:
	// p.Stop() runs completion callbacks and retires the process, both of which
	// re-acquire o.mu — holding it here would deadlock.
	done := make(chan struct{})
	go func() {
		for _, p := range o.List() {
			p.Stop()
		}
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

	// Free the process from the registry (bounded retention).
	o.retireProcess(p)
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

	// Free the process from the registry (bounded retention). Any restart
	// creates a fresh process; the failed one is dead and must not linger.
	o.retireProcess(p)

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

// rateLimiterFor returns the configured per-model rate limiter, or nil if none.
// o.rateLimits is populated only at construction (WithRateLimits) and never
// mutated afterward, so a lock-free read is safe.
func (o *Orchestrator) rateLimiterFor(model string) *rateLimiter {
	return o.rateLimits[model]
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
