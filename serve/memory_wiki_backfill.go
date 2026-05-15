package serve

import (
	"fmt"
)

// Refs govega#71 follow-up. The v1 wiki migration ran once at boot, after
// which any `remember` calls landed in `memory_items` only — and the
// /memory UI reads from `memory_pages`, so those rows were invisible.
// (Concrete symptom: user added Marcel + Brittany via Charlie; Charlie
// could `recall` them in chat but /memory showed nothing.)
//
// v2 dumps every `memory_items` row into a user-scope `legacy-items.md`
// page so they surface in the wiki UI. Idempotent — gated by a separate
// settings flag so we never clobber Mira's refile work.

const wikiBackfillV2SettingKey = "memory_wiki_backfilled_v2"

// BackfillReport summarises what a backfill run did. UsersTouched counts
// the number of distinct user_ids that received a user-scope page.
type BackfillReport struct {
	JustApplied    bool
	AlreadyApplied bool
	PagesWritten   int
	UsersTouched   int
}

// backfillUserScopeLegacyItems writes every memory_items row to a
// user-scope legacy-items.md page (one per userID). Runs once per
// instance — re-running is a no-op. Hand-edits made between runs are
// preserved because the flag gate stops the second pass before any
// writes happen.
func backfillUserScopeLegacyItems(store Store) (BackfillReport, error) {
	var report BackfillReport

	flag, err := store.GetSetting(wikiBackfillV2SettingKey)
	if err != nil {
		return report, fmt.Errorf("read backfill flag: %w", err)
	}
	if flag != nil && flag.Value == "1" {
		report.AlreadyApplied = true
		return report, nil
	}

	items, err := store.ListAllMemoryItems()
	if err != nil {
		return report, fmt.Errorf("list memory_items: %w", err)
	}

	byUser := map[string][]MemoryItem{}
	for _, it := range items {
		byUser[it.UserID] = append(byUser[it.UserID], it)
	}

	for uid, its := range byUser {
		body := renderLegacyItems(its)
		if err := store.UpsertMemoryPage(MemoryPage{
			Scope: MemoryScopeUser, ScopeID: uid, UserID: uid,
			Path:        "legacy-items.md",
			Content:     body,
			Frontmatter: "source: backfill v2 — memory_items union (govega#71)",
		}); err != nil {
			return report, fmt.Errorf("write legacy-items for %s: %w", uid, err)
		}
		report.PagesWritten++
		report.UsersTouched++
	}

	if err := store.UpsertSetting(Setting{Key: wikiBackfillV2SettingKey, Value: "1"}); err != nil {
		return report, fmt.Errorf("set backfill flag: %w", err)
	}
	report.JustApplied = true
	return report, nil
}
