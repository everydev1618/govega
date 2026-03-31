package vega

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONPersistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	p := NewJSONPersistence(path)

	states := []ProcessState{
		{
			ID:        "proc-1",
			AgentName: "Agent1",
			Task:      "Build API",
			WorkDir:   "/tmp/work",
			Status:    StatusRunning,
			StartedAt: time.Now(),
			Metrics: ProcessMetrics{
				Iterations:  5,
				InputTokens: 1000,
				CostUSD:     0.05,
			},
		},
		{
			ID:        "proc-2",
			AgentName: "Agent2",
			Status:    StatusCompleted,
		},
	}

	err := p.Save(states)
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("State file was not created")
	}

	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("Loaded states count = %d, want 2", len(loaded))
	}

	if loaded[0].ID != "proc-1" {
		t.Errorf("First state ID = %q, want %q", loaded[0].ID, "proc-1")
	}
	if loaded[0].AgentName != "Agent1" {
		t.Errorf("First state AgentName = %q, want %q", loaded[0].AgentName, "Agent1")
	}
	if loaded[0].Metrics.Iterations != 5 {
		t.Errorf("First state Iterations = %d, want 5", loaded[0].Metrics.Iterations)
	}
	if loaded[1].Status != StatusCompleted {
		t.Errorf("Second state Status = %q, want %q", loaded[1].Status, StatusCompleted)
	}
}

func TestJSONPersistence_LoadNonexistent(t *testing.T) {
	p := NewJSONPersistence("/tmp/nonexistent_test_file_12345.json")

	states, err := p.Load()
	if err != nil {
		t.Fatalf("Load() for non-existent file should not error: %v", err)
	}
	if states != nil {
		t.Errorf("Load() for non-existent file should return nil, got %v", states)
	}
}

func TestJSONPersistence_LoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	os.WriteFile(path, []byte("not json{{{"), 0644)

	p := NewJSONPersistence(path)
	_, err := p.Load()
	if err == nil {
		t.Error("Load() with invalid JSON should error")
	}
}

func TestJSONPersistence_SaveEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")

	p := NewJSONPersistence(path)

	err := p.Save([]ProcessState{})
	if err != nil {
		t.Fatalf("Save() empty error: %v", err)
	}

	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("Loaded count = %d, want 0", len(loaded))
	}
}

func TestJSONPersistence_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	p := NewJSONPersistence(path)

	// Save initial state
	p.Save([]ProcessState{{ID: "proc-1", Status: StatusRunning}})

	// Overwrite with new state
	p.Save([]ProcessState{{ID: "proc-2", Status: StatusCompleted}})

	loaded, _ := p.Load()
	if len(loaded) != 1 {
		t.Fatalf("Loaded count = %d, want 1", len(loaded))
	}
	if loaded[0].ID != "proc-2" {
		t.Errorf("Loaded ID = %q, want %q", loaded[0].ID, "proc-2")
	}
}

func TestOrchestratorPersistState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	p := NewJSONPersistence(path)

	o := NewOrchestrator(WithLLM(&mockLLM{}), WithPersistence(p))

	agent := Agent{Name: "TestAgent"}
	proc, _ := o.Spawn(agent, WithTask("test task"))

	// persistState is called automatically by Spawn
	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("Loaded states count = %d, want 1", len(loaded))
	}
	if loaded[0].ID != proc.ID {
		t.Errorf("Loaded ID = %q, want %q", loaded[0].ID, proc.ID)
	}
	if loaded[0].AgentName != "TestAgent" {
		t.Errorf("Loaded AgentName = %q, want %q", loaded[0].AgentName, "TestAgent")
	}
}

func TestOrchestratorPersistState_NoPersistence(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{}))

	// Should not panic
	o.persistState()
}
