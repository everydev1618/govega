package serve

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests exercise the wiki-memory primitives against both SQLite
// and (when configured) Postgres. Refs govega#71.

func mustUpsertPage(t *testing.T, store Store, p MemoryPage) {
	t.Helper()
	if err := store.UpsertMemoryPage(p); err != nil {
		t.Fatalf("UpsertMemoryPage %q: %v", p.Path, err)
	}
}

func TestDualStore_MemoryPagesRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{
			Scope:       MemoryScopeUser,
			ScopeID:     "et",
			UserID:      "et",
			Path:        "MEMORY.md",
			Content:     "- [profile](profile.md)\n",
			Frontmatter: "name: index\n",
		})

		got, err := store.GetMemoryPage(MemoryScopeUser, "et", "et", "MEMORY.md")
		if err != nil {
			t.Fatalf("GetMemoryPage: %v", err)
		}
		if got == nil {
			t.Fatal("got nil page, want one")
		}
		if got.Content != "- [profile](profile.md)\n" {
			t.Errorf("content mismatch: %q", got.Content)
		}
		if got.Frontmatter != "name: index\n" {
			t.Errorf("frontmatter mismatch: %q", got.Frontmatter)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Errorf("timestamps not populated: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
		}

		// Missing page → nil, nil.
		nope, err := store.GetMemoryPage(MemoryScopeUser, "et", "et", "does-not-exist.md")
		if err != nil {
			t.Fatalf("GetMemoryPage missing: %v", err)
		}
		if nope != nil {
			t.Errorf("missing page returned %+v, want nil", nope)
		}
	})
}

func TestDualStore_MemoryPagesUpsertPreservesCreatedAt(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{
			Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
			Path: "profile.md", Content: "v1",
		})
		first, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "profile.md")
		if first == nil {
			t.Fatal("first read returned nil")
		}

		// CURRENT_TIMESTAMP in SQLite is second-resolution, so sleep past
		// the next tick before re-upserting or the timestamps collide.
		time.Sleep(1100 * time.Millisecond)

		mustUpsertPage(t, store, MemoryPage{
			Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
			Path: "profile.md", Content: "v2",
		})
		second, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "profile.md")
		if second == nil {
			t.Fatal("second read returned nil")
		}
		if second.Content != "v2" {
			t.Errorf("content not updated: %q", second.Content)
		}
		if !second.CreatedAt.Equal(first.CreatedAt) {
			t.Errorf("CreatedAt changed across upsert: %v → %v", first.CreatedAt, second.CreatedAt)
		}
		if !second.UpdatedAt.After(first.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: %v → %v", first.UpdatedAt, second.UpdatedAt)
		}
	})
}

func TestDualStore_MemoryPagesListPrefix(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		pages := []MemoryPage{
			{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "idx"},
			{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "topics/sushi.md", Content: "sushi"},
			{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "topics/apex.md", Content: "apex"},
			{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "people/alice.md", Content: "alice"},
			// Different scope — must NOT appear in user-scope queries.
			{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "topics/sushi.md", Content: "tony's view"},
		}
		for _, p := range pages {
			mustUpsertPage(t, store, p)
		}

		all, err := store.ListMemoryPages(MemoryScopeUser, "et", "et", "")
		if err != nil {
			t.Fatalf("ListMemoryPages: %v", err)
		}
		if len(all) != 4 {
			t.Errorf("user-scope list len = %d, want 4", len(all))
		}

		topics, _ := store.ListMemoryPages(MemoryScopeUser, "et", "et", "topics/")
		if len(topics) != 2 {
			t.Errorf("topics/ prefix len = %d, want 2", len(topics))
		}
		paths := make([]string, 0, len(topics))
		for _, p := range topics {
			paths = append(paths, p.Path)
		}
		sort.Strings(paths)
		want := []string{"topics/apex.md", "topics/sushi.md"}
		for i, w := range want {
			if i >= len(paths) || paths[i] != w {
				t.Errorf("prefix paths = %v, want %v", paths, want)
				break
			}
		}

		// Agent scope is isolated.
		agentPages, _ := store.ListMemoryPages(MemoryScopeAgent, "tony", "et", "")
		if len(agentPages) != 1 || agentPages[0].Path != "topics/sushi.md" {
			t.Errorf("agent-scope list = %+v, want one topics/sushi.md", agentPages)
		}
	})
}

func TestDualStore_MemoryPagesDelete(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{
			Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
			Path: "scratch.md", Content: "x",
		})
		if err := store.DeleteMemoryPage(MemoryScopeUser, "et", "et", "scratch.md"); err != nil {
			t.Fatalf("DeleteMemoryPage: %v", err)
		}
		got, err := store.GetMemoryPage(MemoryScopeUser, "et", "et", "scratch.md")
		if err != nil {
			t.Fatalf("Get after delete: %v", err)
		}
		if got != nil {
			t.Errorf("page still present after delete: %+v", got)
		}

		// Deleting a missing path is a no-op (no error).
		if err := store.DeleteMemoryPage(MemoryScopeUser, "et", "et", "never-existed.md"); err != nil {
			t.Errorf("delete of missing path errored: %v", err)
		}
	})
}

func TestDualStore_MemoryLinksReplaceAndList(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "idx"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "a.md", Content: "a"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "b.md", Content: "b"})

		// Initial: MEMORY.md → a.md, b.md
		if err := store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "MEMORY.md", []string{"a.md", "b.md"}); err != nil {
			t.Fatalf("ReplaceMemoryLinks (initial): %v", err)
		}
		links, err := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
		if err != nil {
			t.Fatalf("ListMemoryLinks: %v", err)
		}
		if len(links) != 2 {
			t.Fatalf("initial links len = %d, want 2 (%+v)", len(links), links)
		}

		// Replace: MEMORY.md → a.md only. Should drop the row pointing at b.md.
		if err := store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "MEMORY.md", []string{"a.md"}); err != nil {
			t.Fatalf("ReplaceMemoryLinks (shrink): %v", err)
		}
		links, _ = store.ListMemoryLinks(MemoryScopeUser, "et", "et")
		if len(links) != 1 || links[0].ToPath != "a.md" {
			t.Errorf("after shrink: %+v, want one row → a.md", links)
		}

		// Replace with empty slice clears all out-edges from the source.
		if err := store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "MEMORY.md", nil); err != nil {
			t.Fatalf("ReplaceMemoryLinks (clear): %v", err)
		}
		links, _ = store.ListMemoryLinks(MemoryScopeUser, "et", "et")
		if len(links) != 0 {
			t.Errorf("after clear: len = %d, want 0", len(links))
		}
	})
}

func TestDualStore_MemoryPagesDeleteCascadesLinks(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "idx"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "a.md", Content: "a"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "b.md", Content: "b"})
		_ = store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "MEMORY.md", []string{"a.md", "b.md"})
		_ = store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "a.md", []string{"b.md"})

		// Delete a.md — should wipe both its outgoing link (a → b) and
		// the incoming link from MEMORY.md → a.
		if err := store.DeleteMemoryPage(MemoryScopeUser, "et", "et", "a.md"); err != nil {
			t.Fatalf("DeleteMemoryPage: %v", err)
		}
		links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
		for _, l := range links {
			if l.FromPath == "a.md" || l.ToPath == "a.md" {
				t.Errorf("link involving a.md survived delete: %+v", l)
			}
		}
		// MEMORY.md → b.md should still exist (the only remaining edge).
		want := MemoryLink{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", FromPath: "MEMORY.md", ToPath: "b.md"}
		found := false
		for _, l := range links {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("MEMORY.md → b.md missing from %+v", links)
		}
	})
}

func TestDualStore_MemoryPagesRenameRewritesLinks(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "[old](old.md)"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "old.md", Content: "stuff"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "sibling.md", Content: "see [old](old.md)"})
		_ = store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "MEMORY.md", []string{"old.md"})
		_ = store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "old.md", []string{"sibling.md"})
		_ = store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", "sibling.md", []string{"old.md"})

		if err := store.RenameMemoryPage(MemoryScopeUser, "et", "et", "old.md", "new.md"); err != nil {
			t.Fatalf("RenameMemoryPage: %v", err)
		}

		// Old path gone, new path present.
		if p, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "old.md"); p != nil {
			t.Errorf("old.md still present: %+v", p)
		}
		newP, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "new.md")
		if newP == nil || newP.Content != "stuff" {
			t.Errorf("new.md not present / content lost: %+v", newP)
		}

		// Every link mentioning old.md should now mention new.md.
		links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
		for _, l := range links {
			if l.FromPath == "old.md" || l.ToPath == "old.md" {
				t.Errorf("stale link survived rename: %+v", l)
			}
		}
		// Concretely we expect: MEMORY.md→new.md, new.md→sibling.md, sibling.md→new.md.
		wantSet := map[string]bool{
			"MEMORY.md→new.md":   false,
			"new.md→sibling.md":  false,
			"sibling.md→new.md":  false,
		}
		for _, l := range links {
			k := l.FromPath + "→" + l.ToPath
			if _, ok := wantSet[k]; ok {
				wantSet[k] = true
			}
		}
		for k, ok := range wantSet {
			if !ok {
				t.Errorf("missing rewritten link %s; got = %+v", k, links)
			}
		}
	})
}

func TestDualStore_MemoryPagesSearch(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "topics/sushi.md", Content: "favorite: omakase at Sushi Masa"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "topics/apex.md", Content: "deploys: fly.io tenant per user"})
		mustUpsertPage(t, store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "people/sarah.md", Content: "sushi night co-host"})

		hits, err := store.SearchMemoryPages(MemoryScopeUser, "et", "et", "sushi", 10)
		if err != nil {
			t.Fatalf("SearchMemoryPages: %v", err)
		}
		// Should match content of sushi.md and people/sarah.md, and path of sushi.md.
		if len(hits) < 2 {
			t.Errorf("hits len = %d, want >= 2: %+v", len(hits), hits)
		}
		for _, h := range hits {
			combined := strings.ToLower(h.Path + " " + h.Content)
			if !strings.Contains(combined, "sushi") {
				t.Errorf("hit %q doesn't contain sushi: %q", h.Path, h.Content)
			}
		}

		// No hits.
		empty, _ := store.SearchMemoryPages(MemoryScopeUser, "et", "et", "thiswordappearsnowhere", 10)
		if len(empty) != 0 {
			t.Errorf("no-match search returned %+v", empty)
		}
	})
}
