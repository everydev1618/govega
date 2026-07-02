package vega

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestUnsupervisedProcessNotAutoRestarted verifies a plain-Spawned process is
// NOT restarted by the orchestrator just because its agent happens to be in
// the agent registry. ChildRestart's zero value collides with Permanent, so
// an unset policy used to read as "always restart" — which double-restarts
// children that a Supervisor is already managing.
func TestUnsupervisedProcessNotAutoRestarted(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{response: "ok"}))

	var spawns atomic.Int32
	o.OnProcessStarted(func(p *Process) { spawns.Add(1) })

	agent := Agent{Name: "plain-agent", System: StaticPrompt("t")}
	o.RegisterAgent(agent) // registered for other reasons (e.g. respawn-by-name)

	proc, err := o.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitForSpawns(t, &spawns, 1)

	proc.Fail(errors.New("crash"))

	// Auto-restart runs async; give it ample time to (wrongly) fire.
	time.Sleep(200 * time.Millisecond)

	if got := spawns.Load(); got != 1 {
		t.Errorf("spawn count = %d, want 1 — orchestrator auto-restarted a process with no restart policy", got)
	}
}

// TestSpawnSupervisedStillRestarts locks in that the explicit path keeps
// working: SpawnSupervised(Permanent) processes are restarted on failure.
func TestSpawnSupervisedStillRestarts(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{response: "ok"}))

	var spawns atomic.Int32
	o.OnProcessStarted(func(p *Process) { spawns.Add(1) })

	proc, err := o.SpawnSupervised(Agent{Name: "supervised-agent", System: StaticPrompt("t")}, Permanent)
	if err != nil {
		t.Fatalf("SpawnSupervised: %v", err)
	}
	waitForSpawns(t, &spawns, 1)

	proc.Fail(errors.New("crash"))

	waitForSpawns(t, &spawns, 2) // Fatals if the replacement never spawns
}
