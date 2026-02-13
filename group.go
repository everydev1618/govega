package vega

import (
	"context"
	"errors"
	"sync"
)

// ProcessGroup enables multi-agent collaboration by grouping related processes.
// Processes can join multiple groups and groups support broadcast operations.
type ProcessGroup struct {
	name    string
	members map[string]*Process // map[processID]*Process
	mu      sync.RWMutex

	// Callbacks for membership changes
	onJoin  []func(*Process)
	onLeave []func(*Process)
}

// GroupMember contains information about a group member.
type GroupMember struct {
	ID     string
	Name   string // Registered name, if any
	Agent  string
	Status Status
}

// NewGroup creates a new process group.
// Groups are typically accessed via the orchestrator's Join/Leave methods.
func NewGroup(name string) *ProcessGroup {
	return &ProcessGroup{
		name:    name,
		members: make(map[string]*Process),
	}
}

// Name returns the group name.
func (g *ProcessGroup) Name() string {
	return g.name
}

// Join adds a process to this group.
// Returns true if the process was added, false if already a member.
func (g *ProcessGroup) Join(p *Process) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.members[p.ID]; exists {
		return false
	}

	g.members[p.ID] = p

	// Store group membership on process
	p.mu.Lock()
	if p.groups == nil {
		p.groups = make(map[string]*ProcessGroup)
	}
	p.groups[g.name] = g
	p.mu.Unlock()

	// Notify join callbacks
	for _, fn := range g.onJoin {
		go fn(p)
	}

	return true
}

// Leave removes a process from this group.
// Returns true if the process was removed, false if not a member.
func (g *ProcessGroup) Leave(p *Process) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.members[p.ID]; !exists {
		return false
	}

	delete(g.members, p.ID)

	// Remove group from process
	p.mu.Lock()
	delete(p.groups, g.name)
	p.mu.Unlock()

	// Notify leave callbacks
	for _, fn := range g.onLeave {
		go fn(p)
	}

	return true
}

// Members returns all processes in this group.
func (g *ProcessGroup) Members() []*Process {
	g.mu.RLock()
	defer g.mu.RUnlock()

	procs := make([]*Process, 0, len(g.members))
	for _, p := range g.members {
		procs = append(procs, p)
	}
	return procs
}

// MemberInfo returns information about all members.
func (g *ProcessGroup) MemberInfo() []GroupMember {
	g.mu.RLock()
	defer g.mu.RUnlock()

	infos := make([]GroupMember, 0, len(g.members))
	for _, p := range g.members {
		info := GroupMember{
			ID:     p.ID,
			Name:   p.Name(),
			Status: p.Status(),
		}
		if p.Agent != nil {
			info.Agent = p.Agent.Name
		}
		infos = append(infos, info)
	}
	return infos
}

// Count returns the number of members in the group.
func (g *ProcessGroup) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.members)
}

// Has checks if a process is a member of this group.
func (g *ProcessGroup) Has(p *Process) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, exists := g.members[p.ID]
	return exists
}

// OnJoin registers a callback for when processes join.
func (g *ProcessGroup) OnJoin(fn func(*Process)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onJoin = append(g.onJoin, fn)
}

// OnLeave registers a callback for when processes leave.
func (g *ProcessGroup) OnLeave(fn func(*Process)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onLeave = append(g.onLeave, fn)
}

// Broadcast sends a message to all group members.
// Returns a map of process ID to result/error.
func (g *ProcessGroup) Broadcast(ctx context.Context, message string) map[string]error {
	members := g.Members()
	results := make(map[string]error, len(members))

	var wg sync.WaitGroup
	var resultsMu sync.Mutex

	for _, p := range members {
		wg.Add(1)
		go func(proc *Process) {
			defer wg.Done()
			_, err := proc.Send(ctx, message)
			resultsMu.Lock()
			results[proc.ID] = err
			resultsMu.Unlock()
		}(p)
	}

	wg.Wait()
	return results
}

// --- Orchestrator Group Methods ---

// JoinGroup adds a process to a named group.
// Creates the group if it doesn't exist.
func (o *Orchestrator) JoinGroup(groupName string, p *Process) {
	o.groupsMu.Lock()
	group, exists := o.groups[groupName]
	if !exists {
		group = NewGroup(groupName)
		o.groups[groupName] = group
	}
	o.groupsMu.Unlock()

	group.Join(p)
}

// LeaveGroup removes a process from a named group.
func (o *Orchestrator) LeaveGroup(groupName string, p *Process) error {
	o.groupsMu.RLock()
	group, exists := o.groups[groupName]
	o.groupsMu.RUnlock()

	if !exists {
		return ErrGroupNotFound
	}

	group.Leave(p)
	return nil
}

// LeaveAllGroups removes a process from all groups.
// This is called automatically when a process exits.
func (o *Orchestrator) LeaveAllGroups(p *Process) {
	p.mu.RLock()
	groups := make([]*ProcessGroup, 0, len(p.groups))
	for _, g := range p.groups {
		groups = append(groups, g)
	}
	p.mu.RUnlock()

	for _, g := range groups {
		g.Leave(p)
	}
}

// GetGroup returns a process group by name.
func (o *Orchestrator) GetGroup(name string) (*ProcessGroup, bool) {
	o.groupsMu.RLock()
	defer o.groupsMu.RUnlock()
	group, exists := o.groups[name]
	return group, exists
}

// GetOrCreateGroup returns a group, creating it if necessary.
func (o *Orchestrator) GetOrCreateGroup(name string) *ProcessGroup {
	o.groupsMu.Lock()
	defer o.groupsMu.Unlock()

	if group, exists := o.groups[name]; exists {
		return group
	}

	group := NewGroup(name)
	o.groups[name] = group
	return group
}

// DeleteGroup removes an empty group.
// Returns error if the group has members.
func (o *Orchestrator) DeleteGroup(name string) error {
	o.groupsMu.Lock()
	defer o.groupsMu.Unlock()

	group, exists := o.groups[name]
	if !exists {
		return ErrGroupNotFound
	}

	if group.Count() > 0 {
		return &ProcessError{Err: errors.New("cannot delete non-empty group")}
	}

	delete(o.groups, name)
	return nil
}

// ListGroups returns the names of all groups.
func (o *Orchestrator) ListGroups() []string {
	o.groupsMu.RLock()
	defer o.groupsMu.RUnlock()

	names := make([]string, 0, len(o.groups))
	for name := range o.groups {
		names = append(names, name)
	}
	return names
}

// GroupMembers returns members of a named group.
func (o *Orchestrator) GroupMembers(groupName string) ([]*Process, error) {
	o.groupsMu.RLock()
	group, exists := o.groups[groupName]
	o.groupsMu.RUnlock()

	if !exists {
		return nil, ErrGroupNotFound
	}

	return group.Members(), nil
}

// BroadcastToGroup sends a message to all members of a group.
func (o *Orchestrator) BroadcastToGroup(ctx context.Context, groupName, message string) (map[string]error, error) {
	o.groupsMu.RLock()
	group, exists := o.groups[groupName]
	o.groupsMu.RUnlock()

	if !exists {
		return nil, ErrGroupNotFound
	}

	return group.Broadcast(ctx, message), nil
}
