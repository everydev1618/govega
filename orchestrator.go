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

	// Named process registry
	names   map[string]*Process
	namesMu sync.RWMutex

	// Agent registry for respawning
	agents   map[string]Agent
	agentsMu sync.RWMutex

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
	// Unregister name if process was named
	if name := p.Name(); name != "" {
		o.Unregister(name)
	}

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
	// Unregister name if process was named
	if name := p.Name(); name != "" {
		o.Unregister(name)
	}

	o.callbackMu.RLock()
	callbacks := make([]func(*Process, error), len(o.onFailed))
	copy(callbacks, o.onFailed)
	o.callbackMu.RUnlock()

	for _, fn := range callbacks {
		go fn(p, err)
	}

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

// --- Supervision Trees ---

// SupervisorStrategy determines how failures affect siblings.
type SupervisorStrategy int

const (
	// OneForOne restarts only the failed child
	OneForOne SupervisorStrategy = iota
	// OneForAll restarts all children when one fails
	OneForAll
	// RestForOne restarts the failed child and all children started after it
	RestForOne
)

// String returns the strategy name.
func (s SupervisorStrategy) String() string {
	switch s {
	case OneForOne:
		return "one_for_one"
	case OneForAll:
		return "one_for_all"
	case RestForOne:
		return "rest_for_one"
	default:
		return "unknown"
	}
}

// ChildRestart determines when a child should be restarted.
type ChildRestart int

const (
	// Permanent children are always restarted
	Permanent ChildRestart = iota
	// Transient children are restarted only on abnormal exit
	Transient
	// Temporary children are never restarted
	Temporary
)

// String returns the restart type name.
func (r ChildRestart) String() string {
	switch r {
	case Permanent:
		return "permanent"
	case Transient:
		return "transient"
	case Temporary:
		return "temporary"
	default:
		return "unknown"
	}
}

// ChildSpec defines how to start and supervise a child process.
type ChildSpec struct {
	// Name is the registered name for this child (optional)
	Name string
	// Agent is the agent definition to spawn
	Agent Agent
	// Restart determines when to restart this child
	Restart ChildRestart
	// Task is the initial task for the child
	Task string
	// SpawnOpts are additional options for spawning
	SpawnOpts []SpawnOption
}

// SupervisorSpec defines a supervision tree configuration.
type SupervisorSpec struct {
	// Strategy determines how failures affect siblings
	Strategy SupervisorStrategy
	// MaxRestarts is the maximum restarts within Window (0 = unlimited)
	MaxRestarts int
	// Window is the time window for counting restarts
	Window time.Duration
	// Children are the child specifications
	Children []ChildSpec
	// Backoff configures delay between restarts
	Backoff BackoffConfig
}

// Supervisor manages a group of child processes with automatic restart.
type Supervisor struct {
	spec         SupervisorSpec
	orchestrator *Orchestrator
	process      *Process // The supervisor's own process (optional)

	children    []*supervisedChild
	childrenMu  sync.RWMutex
	failures    []time.Time
	failuresMu  sync.Mutex
	restarts    int
	lastBackoff time.Duration

	ctx    context.Context
	cancel context.CancelFunc
}

// supervisedChild tracks a supervised process.
type supervisedChild struct {
	spec    ChildSpec
	process *Process
	index   int // Position in children slice (for RestForOne)
}

// NewSupervisor creates a new supervisor with the given spec.
func (o *Orchestrator) NewSupervisor(spec SupervisorSpec) *Supervisor {
	ctx, cancel := context.WithCancel(o.ctx)
	return &Supervisor{
		spec:         spec,
		orchestrator: o,
		children:     make([]*supervisedChild, 0, len(spec.Children)),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start spawns all children and begins supervision.
func (s *Supervisor) Start() error {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	for i, childSpec := range s.spec.Children {
		child, err := s.spawnChild(childSpec, i)
		if err != nil {
			// Shutdown already-started children
			s.stopAllChildrenLocked()
			return err
		}
		s.children = append(s.children, child)
	}

	return nil
}

// spawnChild spawns a single child and sets up monitoring.
func (s *Supervisor) spawnChild(spec ChildSpec, index int) (*supervisedChild, error) {
	// Build spawn options
	opts := make([]SpawnOption, 0, len(spec.SpawnOpts)+1)
	opts = append(opts, spec.SpawnOpts...)
	if spec.Task != "" {
		opts = append(opts, WithTask(spec.Task))
	}

	// Spawn the process
	proc, err := s.orchestrator.Spawn(spec.Agent, opts...)
	if err != nil {
		return nil, err
	}

	// Register name if specified
	if spec.Name != "" {
		if err := s.orchestrator.Register(spec.Name, proc); err != nil {
			proc.Stop()
			return nil, err
		}
	}

	child := &supervisedChild{
		spec:    spec,
		process: proc,
		index:   index,
	}

	// Set up monitoring from supervisor
	proc.SetTrapExit(false) // Children don't trap exits
	s.monitorChild(child)

	return child, nil
}

// monitorChild sets up exit monitoring for a child.
func (s *Supervisor) monitorChild(child *supervisedChild) {
	// We'll use the orchestrator's OnProcessFailed callback mechanism
	// plus direct monitoring
	go func() {
		proc := child.process

		// Wait for process to complete or fail
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				status := proc.Status()
				if status == StatusCompleted || status == StatusFailed {
					s.handleChildExit(child, status)
					return
				}
			}
		}
	}()
}

// handleChildExit is called when a supervised child exits.
func (s *Supervisor) handleChildExit(child *supervisedChild, status Status) {
	// Determine if we should restart
	shouldRestart := false
	switch child.spec.Restart {
	case Permanent:
		shouldRestart = true
	case Transient:
		shouldRestart = status == StatusFailed
	case Temporary:
		shouldRestart = false
	}

	if !shouldRestart {
		return
	}

	// Check restart limits
	if !s.canRestart() {
		// Exceeded max restarts - supervisor gives up
		s.Stop()
		return
	}

	// Calculate backoff
	backoff := s.calculateBackoff()
	if backoff > 0 {
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}
	}

	// Apply restart strategy
	switch s.spec.Strategy {
	case OneForOne:
		s.restartChild(child)
	case OneForAll:
		s.restartAllChildren()
	case RestForOne:
		s.restartChildAndFollowing(child)
	}
}

// canRestart checks if we're within restart limits.
func (s *Supervisor) canRestart() bool {
	if s.spec.MaxRestarts <= 0 {
		return true // Unlimited
	}

	s.failuresMu.Lock()
	defer s.failuresMu.Unlock()

	now := time.Now()
	s.failures = append(s.failures, now)

	// Prune old failures outside window
	if s.spec.Window > 0 {
		cutoff := now.Add(-s.spec.Window)
		newFailures := make([]time.Time, 0, len(s.failures))
		for _, t := range s.failures {
			if t.After(cutoff) {
				newFailures = append(newFailures, t)
			}
		}
		s.failures = newFailures
	}

	return len(s.failures) <= s.spec.MaxRestarts
}

// calculateBackoff returns the delay before next restart.
func (s *Supervisor) calculateBackoff() time.Duration {
	s.failuresMu.Lock()
	defer s.failuresMu.Unlock()

	s.restarts++

	if s.spec.Backoff.Initial == 0 {
		return 0
	}

	var delay time.Duration
	switch s.spec.Backoff.Type {
	case BackoffExponential:
		multiplier := s.spec.Backoff.Multiplier
		if multiplier == 0 {
			multiplier = 2.0
		}
		delay = time.Duration(float64(s.spec.Backoff.Initial) * pow(multiplier, float64(s.restarts-1)))
	case BackoffLinear:
		delay = s.spec.Backoff.Initial * time.Duration(s.restarts)
	case BackoffConstant:
		delay = s.spec.Backoff.Initial
	}

	if s.spec.Backoff.Max > 0 && delay > s.spec.Backoff.Max {
		delay = s.spec.Backoff.Max
	}

	s.lastBackoff = delay
	return delay
}

// pow is a simple power function for floats.
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// restartChild restarts a single child.
func (s *Supervisor) restartChild(child *supervisedChild) {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	// Stop old process if still running
	if child.process.Status() == StatusRunning {
		child.process.Stop()
	}

	// Unregister old name
	if child.spec.Name != "" {
		s.orchestrator.Unregister(child.spec.Name)
	}

	// Spawn new process
	newChild, err := s.spawnChild(child.spec, child.index)
	if err != nil {
		// Failed to restart - will be handled by next failure
		return
	}

	// Update child reference
	for i, c := range s.children {
		if c == child {
			s.children[i] = newChild
			break
		}
	}
}

// restartAllChildren stops and restarts all children (OneForAll).
func (s *Supervisor) restartAllChildren() {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	// Stop all children in reverse order
	for i := len(s.children) - 1; i >= 0; i-- {
		child := s.children[i]
		if child.process.Status() == StatusRunning {
			child.process.Stop()
		}
		if child.spec.Name != "" {
			s.orchestrator.Unregister(child.spec.Name)
		}
	}

	// Clear children slice
	s.children = s.children[:0]

	// Restart all children in order
	for i, childSpec := range s.spec.Children {
		newChild, err := s.spawnChild(childSpec, i)
		if err != nil {
			// Failed to restart - will be handled by next failure
			continue
		}
		s.children = append(s.children, newChild)
	}
}

// restartChildAndFollowing restarts the failed child and all after it (RestForOne).
func (s *Supervisor) restartChildAndFollowing(failed *supervisedChild) {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	// Find the index of the failed child
	failedIndex := failed.index

	// Stop all children from failedIndex onwards in reverse order
	for i := len(s.children) - 1; i >= failedIndex; i-- {
		child := s.children[i]
		if child.process.Status() == StatusRunning {
			child.process.Stop()
		}
		if child.spec.Name != "" {
			s.orchestrator.Unregister(child.spec.Name)
		}
	}

	// Truncate children slice
	s.children = s.children[:failedIndex]

	// Restart from failedIndex onwards
	for i := failedIndex; i < len(s.spec.Children); i++ {
		newChild, err := s.spawnChild(s.spec.Children[i], i)
		if err != nil {
			continue
		}
		s.children = append(s.children, newChild)
	}
}

// Stop stops the supervisor and all its children.
func (s *Supervisor) Stop() {
	s.cancel()

	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	s.stopAllChildrenLocked()
}

// stopAllChildrenLocked stops all children (must hold childrenMu).
func (s *Supervisor) stopAllChildrenLocked() {
	// Stop in reverse order
	for i := len(s.children) - 1; i >= 0; i-- {
		child := s.children[i]
		if child.process.Status() == StatusRunning {
			child.process.Stop()
		}
		if child.spec.Name != "" {
			s.orchestrator.Unregister(child.spec.Name)
		}
	}
	s.children = nil
}

// Children returns the current supervised processes.
func (s *Supervisor) Children() []*Process {
	s.childrenMu.RLock()
	defer s.childrenMu.RUnlock()

	procs := make([]*Process, len(s.children))
	for i, child := range s.children {
		procs[i] = child.process
	}
	return procs
}

// --- Automatic Restart Integration ---

// SpawnSupervised spawns a process with automatic restart on failure.
// The agent must be registered with RegisterAgent for restart to work.
func (o *Orchestrator) SpawnSupervised(agent Agent, restart ChildRestart, opts ...SpawnOption) (*Process, error) {
	// Register agent for respawning
	o.RegisterAgent(agent)

	// Spawn the process
	proc, err := o.Spawn(agent, opts...)
	if err != nil {
		return nil, err
	}

	// Store restart policy on process
	proc.mu.Lock()
	proc.restartPolicy = restart
	proc.spawnOpts = opts
	proc.mu.Unlock()

	return proc, nil
}

// handleAutoRestart is called when a supervised process fails.
// It should be called from emitFailed.
func (o *Orchestrator) handleAutoRestart(p *Process, err error) {
	p.mu.RLock()
	restartPolicy := p.restartPolicy
	spawnOpts := p.spawnOpts
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}
	procName := p.name
	p.mu.RUnlock()

	// Check if we should restart
	shouldRestart := false
	switch restartPolicy {
	case Permanent:
		shouldRestart = true
	case Transient:
		// Restart on error, not on normal completion
		shouldRestart = true
	case Temporary:
		shouldRestart = false
	default:
		// No restart policy set
		return
	}

	if !shouldRestart {
		return
	}

	// Get the agent definition
	agent, ok := o.GetAgent(agentName)
	if !ok {
		return // Can't restart without agent definition
	}

	// Check supervision policy
	if p.Supervision != nil {
		if !p.Supervision.recordFailure(p, err) {
			return // Max restarts exceeded
		}

		// Calculate and apply backoff
		backoff := p.Supervision.prepareRestart(p)
		if backoff > 0 {
			time.Sleep(backoff)
		}
	}

	// Spawn replacement
	go func() {
		newProc, spawnErr := o.Spawn(agent, spawnOpts...)
		if spawnErr != nil {
			return
		}

		// Copy restart settings to new process
		newProc.mu.Lock()
		newProc.restartPolicy = restartPolicy
		newProc.spawnOpts = spawnOpts
		newProc.Supervision = p.Supervision
		newProc.mu.Unlock()

		// Re-register name if was named
		if procName != "" {
			o.Register(procName, newProc)
		}
	}()
}
