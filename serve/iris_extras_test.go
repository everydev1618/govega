package serve

import (
	"testing"
)

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestIrisExtras_HasWikiNotLegacy pins the memory-tools cutover (govega#71
// follow-up). Before: Iris carried `remember`/`recall`/`forget`, which write
// to the legacy `memory_items` table. The /memory wiki UI only reads from
// `memory_pages`, so anything Iris stored was invisible there — the user
// could ask "who's in my contacts?" and get Marcel + Brittany back, but the
// wiki at /memory showed nothing. This test pins that Iris's extras are the
// wiki tools (memory_read/list/search/write/append/edit) and explicitly NOT
// the legacy three.
func TestIrisExtras_HasWikiNotLegacy(t *testing.T) {
	got := irisExtras(false)

	wantPresent := []string{
		"memory_read", "memory_list", "memory_search",
		"memory_write", "memory_append", "memory_edit",
	}
	for _, w := range wantPresent {
		if !sliceContains(got, w) {
			t.Errorf("irisExtras missing wiki tool %q (got %v)", w, got)
		}
	}

	wantAbsent := []string{"remember", "recall", "forget"}
	for _, w := range wantAbsent {
		if sliceContains(got, w) {
			t.Errorf("irisExtras still carries legacy tool %q — cutover incomplete (got %v)", w, got)
		}
	}

	// Inbox + task tools should survive the cutover.
	for _, w := range []string{"list_inbox", "resolve_inbox", "list_unassigned_tasks", "assign_task", "create_task"} {
		if !sliceContains(got, w) {
			t.Errorf("irisExtras lost unrelated tool %q during memory cutover (got %v)", w, got)
		}
	}
}

// TestIrisExtras_ConfiguredTools folds in host-configured orchestrator tools
// (e.g. an embedding product's search_my_memory) on top of the built-in
// extras — without them, a tool registered on the interpreter is never added
// to the orchestrator's allow-list and the agent silently can't call it.
func TestIrisExtras_ConfiguredTools(t *testing.T) {
	got := irisExtras(false, "search_my_memory")

	if !sliceContains(got, "search_my_memory") {
		t.Errorf("configured tool not included (got %v)", got)
	}
	// Built-in extras must survive alongside configured ones.
	if !sliceContains(got, "memory_read") {
		t.Errorf("built-in extras dropped when configured tools added (got %v)", got)
	}
	// Peering stays gated regardless of configured tools.
	for _, name := range peeringToolNames {
		if sliceContains(got, name) {
			t.Errorf("peering tool %q leaked with peering disabled (got %v)", name, got)
		}
	}
	// No configured tools => no empty entries.
	if sliceContains(irisExtras(false), "") {
		t.Errorf("empty tool name leaked in with no configured tools")
	}
}

// TestIrisExtras_PeeringGated keeps the federation toggle honest: peering
// tools only appear when the caller passes peeringEnabled=true.
func TestIrisExtras_PeeringGated(t *testing.T) {
	off := irisExtras(false)
	on := irisExtras(true)

	if len(on) <= len(off) {
		t.Fatalf("peering=true should add tools (off=%d, on=%d)", len(off), len(on))
	}
	for _, name := range peeringToolNames {
		if sliceContains(off, name) {
			t.Errorf("peering=false should not include %q", name)
		}
		if !sliceContains(on, name) {
			t.Errorf("peering=true should include %q", name)
		}
	}
}
