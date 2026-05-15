package dsl

import (
	"strconv"
	"testing"
)

// TestInboxLoop_Stabilization is the integration test for the combined
// fix that closed the orchestrator's token-burn loop:
//
//  1. Auto-success outcomes never enter the pending queue
//     (recordDispatchOutcome → InsertResolvedInboxItem).
//  2. Identical pending dispatch outcomes dedupe instead of stacking
//     (insertDispatchOutcome scans by (from_agent, subject)).
//  3. After DispatchTriageThreshold reads without an orchestrator
//     decision, items auto-age out
//     (TriageInboxItems + the list_inbox tool wrapper).
//
// The failure mode we're guarding against is a runaway pending queue:
// an agent keeps trailing off mid-thought, the orchestrator keeps
// reading the item but can't decide, and every heartbeat re-pays the
// LLM cost on the same item — sometimes growing the queue as new
// dispatches add more.
//
// This test simulates a realistic burst: scout dispatched 5 times with
// "trail-off mid-thought" outcomes interleaved with 5 successful runs,
// followed by 4 orchestrator "reads" (no resolutions). After all that:
//   - the pending queue MUST be at most 1 deduped item.
//   - after the threshold-th read it MUST have auto-aged to zero.
//   - the resolved/Done count grows monotonically with successes +
//     auto-ages.
func TestInboxLoop_Stabilization(t *testing.T) {
	b := &fakeInboxBackend{}

	// Phase 1: a burst of 10 dispatches — 5 successes + 5 trail-offs.
	// All trail-offs produce the same (agent, subject) so dedupe should
	// collapse them to a single pending row.
	for i := 0; i < 5; i++ {
		// Success
		_, err := recordDispatchOutcome(b, "scout", "Task completed by scout", "Result: ok run "+strconv.Itoa(i), "normal")
		if err != nil {
			t.Fatalf("success record #%d: %v", i, err)
		}
		// Trail-off (identical subject across all 5 to exercise dedupe)
		_, err = recordDispatchOutcome(b, "scout", "Task may be incomplete from scout", "trail-off body "+strconv.Itoa(i), "urgent")
		if err != nil {
			t.Fatalf("trail-off record #%d: %v", i, err)
		}
	}

	pending := pendingItems(b)
	if len(pending) != 1 {
		t.Errorf("after 10 dispatches (5 success + 5 trail-off-dups), pending = %d, want 1", len(pending))
	}
	resolved := resolvedItems(b)
	if len(resolved) != 5 {
		t.Errorf("after 5 successes, resolved = %d, want 5", len(resolved))
	}
	// Successes must not be sitting in pending.
	for _, it := range pending {
		if it.Subject == "Task completed by scout" {
			t.Errorf("success leaked into pending: %+v", it)
		}
	}

	// Phase 2: simulate orchestrator "reads" without resolving. Each
	// read calls TriageInboxItems for the pending ids — the exact same
	// wiring the list_inbox tool uses. After DispatchTriageThreshold-1
	// reads, the item must still be pending. On the threshold-th read
	// it auto-ages.
	if DispatchTriageThreshold < 2 {
		t.Fatalf("test assumes DispatchTriageThreshold >= 2, got %d", DispatchTriageThreshold)
	}
	for read := 1; read < DispatchTriageThreshold; read++ {
		simulateOrchestratorRead(t, b)
		if got := len(pendingItems(b)); got != 1 {
			t.Errorf("read #%d: pending = %d, want 1 (auto-age should not have fired yet)", read, got)
		}
	}

	// The threshold-th read auto-ages the item.
	simulateOrchestratorRead(t, b)
	if got := len(pendingItems(b)); got != 0 {
		t.Errorf("after threshold-th read: pending = %d, want 0 (item should have auto-aged)", got)
	}

	// Phase 3: another burst of identical trail-offs after auto-age.
	// Since the prior item is now resolved, dedupe doesn't block — a
	// fresh pending row is allowed. Queue stays bounded at 1.
	for i := 0; i < 3; i++ {
		_, err := recordDispatchOutcome(b, "scout", "Task may be incomplete from scout", "trail-off again "+strconv.Itoa(i), "urgent")
		if err != nil {
			t.Fatalf("second-burst trail-off #%d: %v", i, err)
		}
	}
	if got := len(pendingItems(b)); got != 1 {
		t.Errorf("after second burst of identical trail-offs, pending = %d, want 1", got)
	}

	// Phase 4: queue-size invariant — across the whole simulation, the
	// pending queue never exceeded 1. The original bug grew the queue
	// linearly with dispatch count (3, 5, 10+ identical urgent cards).
	// We can't observe "max-ever" without instrumentation, but the
	// current count is the only thing the orchestrator pays tokens on,
	// so asserting bounded final state is the load-bearing check.
	if got := len(pendingItems(b)); got > 1 {
		t.Errorf("final pending queue exceeded bound: %d (want ≤ 1)", got)
	}
}

// TestInboxLoop_DifferentAgentsDoNotDedupe pins the dedupe scope:
// "Task may be incomplete from scout" and "Task may be incomplete from
// remy" are separate concerns and must both surface to the orchestrator
// at the same time.
func TestInboxLoop_DifferentAgentsDoNotDedupe(t *testing.T) {
	b := &fakeInboxBackend{}
	for _, agent := range []string{"scout", "remy", "nadia"} {
		_, err := recordDispatchOutcome(b, agent, "Task may be incomplete from "+agent, "trail", "urgent")
		if err != nil {
			t.Fatalf("record for %s: %v", agent, err)
		}
	}
	if got := len(pendingItems(b)); got != 3 {
		t.Errorf("3 different agents should produce 3 pending items, got %d", got)
	}
}

// TestInboxLoop_FailureAndIncompleteAreNotDedupedAgainstEachOther
// covers the subtler case where the same agent flips between failure
// flavors. "Task failed for scout" and "Task may be incomplete from
// scout" carry different remediation paths — must not collapse.
func TestInboxLoop_FailureAndIncompleteAreNotDedupedAgainstEachOther(t *testing.T) {
	b := &fakeInboxBackend{}
	_, _ = recordDispatchOutcome(b, "scout", "Task may be incomplete from scout", "trail", "urgent")
	_, _ = recordDispatchOutcome(b, "scout", "Task failed for scout", "error", "urgent")
	if got := len(pendingItems(b)); got != 2 {
		t.Errorf("different failure subjects for same agent should produce 2 items, got %d", got)
	}
}

// simulateOrchestratorRead mirrors the inner loop of the list_inbox
// tool (inbox_tools.go): list pending, triage the ids returned, drop
// auto-aged ones from the result. The orchestrator's "decision" step
// is deliberately omitted — that's what we're testing the backstop
// against.
func simulateOrchestratorRead(t *testing.T, b *fakeInboxBackend) {
	t.Helper()
	items, err := b.ListInboxItems("pending", 50)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	if _, err := b.TriageInboxItems(ids, DispatchTriageThreshold); err != nil {
		t.Fatalf("triage: %v", err)
	}
}

func pendingItems(b *fakeInboxBackend) []InboxItem {
	out := make([]InboxItem, 0)
	for _, it := range b.items {
		if it.Status == "pending" {
			out = append(out, it)
		}
	}
	return out
}

func resolvedItems(b *fakeInboxBackend) []InboxItem {
	out := make([]InboxItem, 0)
	for _, it := range b.items {
		if it.Status == "resolved" {
			out = append(out, it)
		}
	}
	return out
}

