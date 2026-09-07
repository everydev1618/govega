package vega

import "testing"

func TestContextPressureHook(t *testing.T) {
	defer SetContextPressureHook(nil)

	// No hook, no compactable context: pressure is unresolved.
	p := &Process{ID: "p1", Agent: &Agent{Name: "a"}}
	if p.tryCompactContext() {
		t.Fatal("no hook and no compactable context should return false")
	}

	// A hook that relieves the pressure (e.g. grew the backing server's
	// window) short-circuits compaction.
	var sawProcess *Process
	SetContextPressureHook(func(proc *Process) bool {
		sawProcess = proc
		return true
	})
	if !p.tryCompactContext() {
		t.Fatal("hook returning true should resolve the pressure")
	}
	if sawProcess != p {
		t.Error("hook did not receive the pressured process")
	}

	// A hook that declines falls through to normal compaction (which fails
	// here: no compactable context).
	SetContextPressureHook(func(proc *Process) bool { return false })
	if p.tryCompactContext() {
		t.Fatal("declining hook with no compactable context should return false")
	}
}
