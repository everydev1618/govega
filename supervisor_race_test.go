package vega

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitForSpawns polls until the async started-callback counter reaches want.
func waitForSpawns(t *testing.T, spawns *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for spawns.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("spawns = %d, want %d", spawns.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSupervisorOneForAllNoRestartStorm verifies that siblings stopped by the
// supervisor during a OneForAll restart do not re-trigger the restart handler
// (H4). One child failure must produce exactly one restart round.
func TestSupervisorOneForAllNoRestartStorm(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{response: "ok"}), WithMaxProcesses(1000))

	var spawns atomic.Int32
	o.OnProcessStarted(func(p *Process) { spawns.Add(1) })

	spec := SupervisorSpec{
		Strategy: OneForAll,
		Children: []ChildSpec{
			{Name: "storm-a", Agent: Agent{Name: "storm-agent-a", System: StaticPrompt("t")}, Restart: Permanent},
			{Name: "storm-b", Agent: Agent{Name: "storm-agent-b", System: StaticPrompt("t")}, Restart: Permanent},
		},
	}
	sup := o.NewSupervisor(spec)
	if err := sup.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sup.Stop()

	// Started callbacks fire async; wait for the initial pair.
	waitForSpawns(t, &spawns, 2)

	sup.GetChild("storm-a").Fail(errors.New("crash"))

	// One restart round should happen within a couple of poll intervals.
	// Wait long enough for a storm (stopped siblings re-triggering restarts
	// every poll interval) to become unmistakable if present.
	time.Sleep(12 * DefaultSupervisorPollInterval)

	if got := spawns.Load(); got != 4 {
		t.Errorf("spawn count = %d, want 4 (2 initial + 2 restarted); supervisor-initiated stops re-triggered restarts", got)
	}

	total, running, _ := sup.CountChildren()
	if total != 2 || running != 2 {
		t.Errorf("children total=%d running=%d, want 2/2", total, running)
	}
}

// TestSupervisorRestForOneNoRestartStorm is the RestForOne variant of the
// restart-storm test: the trailing child stopped by the supervisor must not
// re-trigger restartChildAndFollowing.
func TestSupervisorRestForOneNoRestartStorm(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{response: "ok"}), WithMaxProcesses(1000))

	var spawns atomic.Int32
	o.OnProcessStarted(func(p *Process) { spawns.Add(1) })

	spec := SupervisorSpec{
		Strategy: RestForOne,
		Children: []ChildSpec{
			{Name: "rest-a", Agent: Agent{Name: "rest-agent-a", System: StaticPrompt("t")}, Restart: Permanent},
			{Name: "rest-b", Agent: Agent{Name: "rest-agent-b", System: StaticPrompt("t")}, Restart: Permanent},
			{Name: "rest-c", Agent: Agent{Name: "rest-agent-c", System: StaticPrompt("t")}, Restart: Permanent},
		},
	}
	sup := o.NewSupervisor(spec)
	if err := sup.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sup.Stop()

	waitForSpawns(t, &spawns, 3)

	// Fail the middle child: b and c restart, a is untouched.
	sup.GetChild("rest-b").Fail(errors.New("crash"))

	time.Sleep(12 * DefaultSupervisorPollInterval)

	if got := spawns.Load(); got != 5 {
		t.Errorf("spawn count = %d, want 5 (3 initial + b and c restarted); supervisor-initiated stops re-triggered restarts", got)
	}

	total, running, _ := sup.CountChildren()
	if total != 3 || running != 3 {
		t.Errorf("children total=%d running=%d, want 3/3", total, running)
	}
}

// TestRestartChildConcurrentDeleteChild exercises the RestartChild window
// where the lock is dropped for spawning: a concurrent DeleteChild shrinks
// the children slice, and the stale index write panics or corrupts state (H5).
func TestRestartChildConcurrentDeleteChild(t *testing.T) {
	o := NewOrchestrator(WithLLM(&mockLLM{response: "ok"}), WithMaxProcesses(1000))

	mkSpec := func(name, agent string, opts ...SpawnOption) ChildSpec {
		return ChildSpec{Name: name, Agent: Agent{Name: agent, System: StaticPrompt("t")}, Restart: Temporary, SpawnOpts: opts}
	}
	// Slow down del-2's spawn so RestartChild reliably sits in its unlocked
	// spawn window while DeleteChild shrinks the children slice.
	slowSpawn := SpawnOption(func(p *Process) { time.Sleep(100 * time.Millisecond) })
	spec := SupervisorSpec{
		Strategy: OneForOne,
		Children: []ChildSpec{
			mkSpec("del-0", "del-agent-0"),
			mkSpec("del-1", "del-agent-1"),
			mkSpec("del-2", "del-agent-2", slowSpawn),
		},
	}
	sup := o.NewSupervisor(spec)
	if err := sup.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// No deferred sup.Stop(): the buggy path panics while holding the
	// supervisor's children lock, which would deadlock cleanup. Stop runs
	// explicitly on the success path below.

	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		if err := sup.RestartChild("del-2"); err != nil {
			t.Errorf("RestartChild: %v", err)
		}
	}()

	// Let RestartChild drop the lock and enter the slow spawn, then delete
	// an earlier child so the remembered index points past the new end.
	time.Sleep(30 * time.Millisecond)
	if err := sup.DeleteChild("del-0"); err != nil {
		t.Fatalf("DeleteChild: %v", err)
	}

	if r := <-done; r != nil {
		t.Fatalf("RestartChild panicked: %v", r)
	}

	// del-2 must still be tracked exactly once, and del-1 untouched.
	counts := map[string]int{}
	for _, info := range sup.WhichChildren() {
		counts[info.Name]++
	}
	if counts["del-2"] != 1 || counts["del-1"] != 1 || counts["del-0"] != 0 {
		t.Errorf("children after churn = %v, want del-1:1 del-2:1 del-0 removed", counts)
	}
	sup.Stop()
}
