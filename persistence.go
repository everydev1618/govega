package vega

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Persistence interface for saving process state.
type Persistence interface {
	Save(states []ProcessState) error
	Load() ([]ProcessState, error)
}

// ProcessState is the persisted state of a process.
type ProcessState struct {
	ID        string         `json:"id"`
	AgentName string         `json:"agent_name"`
	Task      string         `json:"task"`
	WorkDir   string         `json:"work_dir"`
	Status    Status         `json:"status"`
	StartedAt time.Time      `json:"started_at"`
	Metrics   ProcessMetrics `json:"metrics"`
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

// persistState saves process state.
func (o *Orchestrator) persistState() {
	if o.persistence == nil {
		return
	}

	o.mu.RLock()
	states := make([]ProcessState, 0, len(o.processes))
	for _, p := range o.processes {
		states = append(states, ProcessState{
			ID:        p.ID,
			AgentName: p.Agent.Name,
			Task:      p.Task,
			WorkDir:   p.WorkDir,
			Status:    p.status,
			StartedAt: p.StartedAt,
			Metrics:   p.metrics,
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

	recoverable := 0
	for _, state := range states {
		if state.Status == StatusRunning || state.Status == StatusPending {
			recoverable++
		}
	}
	if recoverable > 0 {
		// Respawning requires registered agent definitions, which aren't
		// available at construction time. Surface the gap rather than
		// silently dropping the work; durable resume is planned.
		slog.Warn("process recovery: in-flight processes found but respawn is not implemented",
			"recoverable", recoverable,
			"total_loaded", len(states),
		)
	}
}
