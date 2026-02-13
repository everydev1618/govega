package vega

import (
	"context"
	"sync"
	"time"
)

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
			case <-time.After(DefaultSupervisorPollInterval):
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

// --- Dynamic Child Management ---

// ChildInfo contains information about a supervised child.
type ChildInfo struct {
	Name    string
	ID      string
	Status  Status
	Restart ChildRestart
	Agent   string
}

// WhichChildren returns information about all current children.
func (s *Supervisor) WhichChildren() []ChildInfo {
	s.childrenMu.RLock()
	defer s.childrenMu.RUnlock()

	infos := make([]ChildInfo, len(s.children))
	for i, child := range s.children {
		infos[i] = ChildInfo{
			Name:    child.spec.Name,
			ID:      child.process.ID,
			Status:  child.process.Status(),
			Restart: child.spec.Restart,
			Agent:   child.spec.Agent.Name,
		}
	}
	return infos
}

// StartChild dynamically adds and starts a new child to the supervisor.
// Returns the new process or an error if the child couldn't be started.
func (s *Supervisor) StartChild(spec ChildSpec) (*Process, error) {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	// Check for duplicate name
	if spec.Name != "" {
		for _, child := range s.children {
			if child.spec.Name == spec.Name {
				return nil, &ProcessError{AgentName: spec.Agent.Name, Err: ErrNameTaken}
			}
		}
	}

	// Spawn the child
	index := len(s.children)
	child, err := s.spawnChild(spec, index)
	if err != nil {
		return nil, err
	}

	// Add to children list
	s.children = append(s.children, child)

	// Add to spec for restart purposes
	s.spec.Children = append(s.spec.Children, spec)

	return child.process, nil
}

// TerminateChild stops a specific child by name.
// The child will be restarted according to its restart policy unless DeleteChild is called.
func (s *Supervisor) TerminateChild(name string) error {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	for _, child := range s.children {
		if child.spec.Name == name {
			if child.process.Status() == StatusRunning {
				child.process.Stop()
			}
			return nil
		}
	}

	return ErrProcessNotFound
}

// RestartChild forces a restart of a specific child by name.
func (s *Supervisor) RestartChild(name string) error {
	s.childrenMu.Lock()

	var targetChild *supervisedChild
	var targetIndex int
	for i, child := range s.children {
		if child.spec.Name == name {
			targetChild = child
			targetIndex = i
			break
		}
	}

	if targetChild == nil {
		s.childrenMu.Unlock()
		return ErrProcessNotFound
	}

	// Stop the current process
	if targetChild.process.Status() == StatusRunning {
		targetChild.process.Stop()
	}

	// Unregister name
	if targetChild.spec.Name != "" {
		s.orchestrator.Unregister(targetChild.spec.Name)
	}

	s.childrenMu.Unlock()

	// Spawn new process (outside lock to avoid deadlock)
	newChild, err := s.spawnChild(targetChild.spec, targetIndex)
	if err != nil {
		return err
	}

	// Update the children slice
	s.childrenMu.Lock()
	s.children[targetIndex] = newChild
	s.childrenMu.Unlock()

	return nil
}

// DeleteChild removes a child from the supervisor entirely.
// The child is stopped if running and will not be restarted.
func (s *Supervisor) DeleteChild(name string) error {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()

	for i, child := range s.children {
		if child.spec.Name == name {
			// Stop if running
			if child.process.Status() == StatusRunning {
				child.process.Stop()
			}

			// Unregister name
			if child.spec.Name != "" {
				s.orchestrator.Unregister(child.spec.Name)
			}

			// Remove from children slice
			s.children = append(s.children[:i], s.children[i+1:]...)

			// Remove from spec
			for j, spec := range s.spec.Children {
				if spec.Name == name {
					s.spec.Children = append(s.spec.Children[:j], s.spec.Children[j+1:]...)
					break
				}
			}

			// Update indices for remaining children
			for j := i; j < len(s.children); j++ {
				s.children[j].index = j
			}

			return nil
		}
	}

	return ErrProcessNotFound
}

// CountChildren returns the number of children (total, running, failed).
func (s *Supervisor) CountChildren() (total, running, failed int) {
	s.childrenMu.RLock()
	defer s.childrenMu.RUnlock()

	total = len(s.children)
	for _, child := range s.children {
		switch child.process.Status() {
		case StatusRunning:
			running++
		case StatusFailed:
			failed++
		}
	}
	return
}

// GetChild returns the process for a specific child by name.
func (s *Supervisor) GetChild(name string) *Process {
	s.childrenMu.RLock()
	defer s.childrenMu.RUnlock()

	for _, child := range s.children {
		if child.spec.Name == name {
			return child.process
		}
	}
	return nil
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
