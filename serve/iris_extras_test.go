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
