package vega

import "time"

// ExitReason describes why a process exited.
type ExitReason string

const (
	// ExitNormal means the process completed successfully
	ExitNormal ExitReason = "normal"
	// ExitError means the process failed with an error
	ExitError ExitReason = "error"
	// ExitKilled means the process was explicitly killed
	ExitKilled ExitReason = "killed"
	// ExitLinked means the process died because a linked process died
	ExitLinked ExitReason = "linked"
)

// ExitSignal is sent to linked/monitoring processes when a process exits.
// When trapExit is true, these are delivered via the ExitSignals channel.
// When trapExit is false, linked process deaths cause this process to die.
type ExitSignal struct {
	// ProcessID is the ID of the process that exited
	ProcessID string
	// AgentName is the name of the agent that was running
	AgentName string
	// Reason explains why the process exited
	Reason ExitReason
	// Error is set if Reason is ExitError
	Error error
	// Result is set if Reason is ExitNormal
	Result string
	// Timestamp is when the exit occurred
	Timestamp time.Time
}

// MonitorRef is a reference to an active monitor, used for demonitoring.
type MonitorRef struct {
	id        uint64
	processID string
}

// monitorEntry tracks a monitoring relationship.
type monitorEntry struct {
	ref     MonitorRef
	process *Process
}

// Link creates a bidirectional link between this process and another.
// If either process dies, the other will also die (unless trapExit is set).
// Linking is idempotent - linking to an already-linked process is a no-op.
func (p *Process) Link(other *Process) {
	if p == other || other == nil {
		return
	}

	// Lock both processes in consistent order to avoid deadlock
	first, second := p, other
	if p.ID > other.ID {
		first, second = other, p
	}

	first.linkMu.Lock()
	second.linkMu.Lock()

	// Initialize maps if needed
	if first.links == nil {
		first.links = make(map[string]*Process)
	}
	if second.links == nil {
		second.links = make(map[string]*Process)
	}

	// Create bidirectional link
	first.links[second.ID] = second
	second.links[first.ID] = first

	second.linkMu.Unlock()
	first.linkMu.Unlock()
}

// Unlink removes the bidirectional link between this process and another.
// Unlinking is idempotent - unlinking from a non-linked process is a no-op.
func (p *Process) Unlink(other *Process) {
	if p == other || other == nil {
		return
	}

	// Lock both processes in consistent order to avoid deadlock
	first, second := p, other
	if p.ID > other.ID {
		first, second = other, p
	}

	first.linkMu.Lock()
	second.linkMu.Lock()

	delete(first.links, second.ID)
	delete(second.links, first.ID)

	second.linkMu.Unlock()
	first.linkMu.Unlock()
}

// Monitor starts monitoring another process.
// When the monitored process exits, this process receives an ExitSignal
// on its ExitSignals channel (does not cause death, unlike Link).
// Returns a MonitorRef that can be used to stop monitoring.
func (p *Process) Monitor(other *Process) MonitorRef {
	if p == other || other == nil {
		return MonitorRef{}
	}

	p.linkMu.Lock()
	defer p.linkMu.Unlock()

	other.linkMu.Lock()
	defer other.linkMu.Unlock()

	// Initialize maps if needed
	if p.monitors == nil {
		p.monitors = make(map[string]*monitorEntry)
	}
	if other.monitoredBy == nil {
		other.monitoredBy = make(map[string]*monitorEntry)
	}
	if p.exitSignals == nil {
		p.exitSignals = make(chan ExitSignal, 16)
	}

	// Generate unique monitor ID
	p.nextMonitorID++
	ref := MonitorRef{
		id:        p.nextMonitorID,
		processID: other.ID,
	}

	entry := &monitorEntry{
		ref:     ref,
		process: p,
	}

	p.monitors[other.ID] = entry
	other.monitoredBy[p.ID] = entry

	return ref
}

// Demonitor stops monitoring a process.
// The MonitorRef must be one returned by a previous Monitor call.
func (p *Process) Demonitor(ref MonitorRef) {
	if ref.processID == "" {
		return
	}

	p.linkMu.Lock()
	entry, ok := p.monitors[ref.processID]
	if !ok || entry.ref.id != ref.id {
		p.linkMu.Unlock()
		return
	}
	delete(p.monitors, ref.processID)
	p.linkMu.Unlock()

	// Find the other process and remove ourselves from monitoredBy
	if p.orchestrator != nil {
		if other := p.orchestrator.Get(ref.processID); other != nil {
			other.linkMu.Lock()
			delete(other.monitoredBy, p.ID)
			other.linkMu.Unlock()
		}
	}
}

// SetTrapExit enables or disables exit trapping.
// When trapExit is true, linked process deaths deliver ExitSignals
// instead of killing this process. This is how supervisors survive
// their children dying.
func (p *Process) SetTrapExit(trap bool) {
	p.linkMu.Lock()
	defer p.linkMu.Unlock()

	p.trapExit = trap
	if trap && p.exitSignals == nil {
		p.exitSignals = make(chan ExitSignal, 16)
	}
}

// TrapExit returns whether exit trapping is enabled.
func (p *Process) TrapExit() bool {
	p.linkMu.RLock()
	defer p.linkMu.RUnlock()
	return p.trapExit
}

// ExitSignals returns the channel for receiving exit signals.
// Only receives signals when trapExit is true, or for monitored processes.
// Returns nil if no exit signal channel has been created.
func (p *Process) ExitSignals() <-chan ExitSignal {
	p.linkMu.RLock()
	defer p.linkMu.RUnlock()
	return p.exitSignals
}

// Links returns the IDs of all linked processes.
func (p *Process) Links() []string {
	p.linkMu.RLock()
	defer p.linkMu.RUnlock()

	ids := make([]string, 0, len(p.links))
	for id := range p.links {
		ids = append(ids, id)
	}
	return ids
}

// propagateExit notifies linked and monitoring processes of this process's death.
func (p *Process) propagateExit(signal ExitSignal) {
	p.linkMu.Lock()

	// Collect linked processes
	linkedProcs := make([]*Process, 0, len(p.links))
	for _, linked := range p.links {
		linkedProcs = append(linkedProcs, linked)
	}

	// Collect monitoring processes
	monitoringProcs := make([]*Process, 0, len(p.monitoredBy))
	for _, entry := range p.monitoredBy {
		monitoringProcs = append(monitoringProcs, entry.process)
	}

	// Clear our links and monitors (we're dead)
	p.links = nil
	p.monitoredBy = nil

	p.linkMu.Unlock()

	// Notify linked processes
	for _, linked := range linkedProcs {
		linked.handleLinkedExit(p, signal)
	}

	// Notify monitoring processes
	for _, monitoring := range monitoringProcs {
		monitoring.handleMonitoredExit(p, signal)
	}
}

// handleLinkedExit is called when a linked process dies.
func (p *Process) handleLinkedExit(dead *Process, signal ExitSignal) {
	// Remove the dead process from our links
	p.linkMu.Lock()
	delete(p.links, dead.ID)
	trapExit := p.trapExit
	exitCh := p.exitSignals
	p.linkMu.Unlock()

	if trapExit && exitCh != nil {
		// Trapping exits - deliver as signal instead of dying
		select {
		case exitCh <- signal:
		default:
			// Channel full, signal dropped
		}
		return
	}

	// Not trapping exits - we die too (unless it was a normal exit)
	if signal.Reason == ExitNormal {
		return // Normal exits don't propagate death
	}

	// Cascade the death
	p.mu.Lock()
	if p.status == StatusCompleted || p.status == StatusFailed {
		p.mu.Unlock()
		return // Already dead
	}
	p.status = StatusFailed
	p.metrics.CompletedAt = time.Now()
	p.mu.Unlock()

	// Propagate with ExitLinked reason
	cascadeSignal := ExitSignal{
		ProcessID: p.ID,
		AgentName: p.Agent.Name,
		Reason:    ExitLinked,
		Error:     &LinkedProcessError{LinkedID: dead.ID, OriginalError: signal.Error},
		Timestamp: time.Now(),
	}
	p.propagateExit(cascadeSignal)

	// Notify orchestrator
	if p.orchestrator != nil {
		p.orchestrator.emitFailed(p, cascadeSignal.Error)
	}
}

// handleMonitoredExit is called when a monitored process dies.
func (p *Process) handleMonitoredExit(dead *Process, signal ExitSignal) {
	// Remove the dead process from our monitors
	p.linkMu.Lock()
	delete(p.monitors, dead.ID)
	exitCh := p.exitSignals
	p.linkMu.Unlock()

	// Monitors always deliver signals (never cause death)
	if exitCh != nil {
		select {
		case exitCh <- signal:
		default:
			// Channel full, signal dropped
		}
	}
}

// LinkedProcessError is the error set when a process dies due to a linked process dying.
type LinkedProcessError struct {
	LinkedID      string
	OriginalError error
}

func (e *LinkedProcessError) Error() string {
	if e.OriginalError != nil {
		return "linked process " + e.LinkedID + " died: " + e.OriginalError.Error()
	}
	return "linked process " + e.LinkedID + " died"
}

func (e *LinkedProcessError) Unwrap() error {
	return e.OriginalError
}
