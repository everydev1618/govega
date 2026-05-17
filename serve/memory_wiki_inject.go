package serve

import (
	"fmt"
	"sort"
	"strings"
)

// wikiInjectionLineLimit caps each injected MEMORY.md at this many
// lines. Anything past the cap is summarized as a truncation marker —
// the LLM follows up with memory_read for the full page.
const wikiInjectionLineLimit = 200

// activeBodyInjectionCap is the maximum number of `active: true`
// note bodies injected alongside the MEMORY.md indices on every
// turn. Active notes — most commonly the freshest compacted session
// note (refs govega#100) — get their full body in the prompt so the
// agent can keep using their substance without an explicit
// memory_read. The cap is a backstop: if many notes accidentally
// carry the flag, prompts don't balloon. Newer notes (by UpdatedAt)
// win when the cap kicks in.
const activeBodyInjectionCap = 3

// activeBodyLineLimit caps each active note body at this many lines
// when injected. Same shape as wikiInjectionLineLimit, just looser
// since active notes are deliberately surfaced and need substance.
const activeBodyLineLimit = 120

// formatWikiMemoryForInjection loads the shared user wiki's MEMORY.md
// and the calling agent's private MEMORY.md, concatenates them with
// clear headers, then appends the bodies of any pages flagged
// `active: true` in frontmatter (most-recent first, capped). Returns
// "" when nothing applies. Refs govega#71, govega#100.
func formatWikiMemoryForInjection(store Store, userID, agent string) string {
	var b strings.Builder

	if p, err := store.GetMemoryPage(MemoryScopeUser, userID, userID, "MEMORY.md"); err == nil && p != nil && strings.TrimSpace(p.Content) != "" {
		b.WriteString("## Your memory (shared across every agent that talks to this user)\n")
		b.WriteString(truncateLines(p.Content, wikiInjectionLineLimit))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}

	if p, err := store.GetMemoryPage(MemoryScopeAgent, agent, userID, "MEMORY.md"); err == nil && p != nil && strings.TrimSpace(p.Content) != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Your private working notes (only you see this)\n")
		b.WriteString(truncateLines(p.Content, wikiInjectionLineLimit))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}

	if active := collectActivePages(store, userID, agent, activeBodyInjectionCap); len(active) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Active notes (auto-surfaced — bodies included so you can keep using them without re-reading)\n")
		for _, p := range active {
			fmt.Fprintf(&b, "\n### %s\n", p.Path)
			b.WriteString(truncateLines(strings.TrimSpace(p.Content), activeBodyLineLimit))
			if !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
		}
	}

	if b.Len() == 0 {
		return ""
	}
	b.WriteString("\nUse `memory_read` to drill into linked pages. Use `memory_write` / `memory_append` / `memory_edit` to update memory. Keep `MEMORY.md` itself short — it's the index, not the storage.")
	return b.String()
}

// collectActivePages returns up to `cap` non-MEMORY.md pages across
// both user and agent scopes whose frontmatter contains
// `active: true`. Ordered newest-first by UpdatedAt so a runaway tag
// spree degrades gracefully — recent work wins. MEMORY.md is
// excluded since its content is already injected via the index path.
func collectActivePages(store Store, userID, agent string, cap int) []MemoryPage {
	candidates := make([]MemoryPage, 0, cap*2)
	for _, src := range []struct {
		scope   MemoryScope
		scopeID string
	}{
		{MemoryScopeUser, userID},
		{MemoryScopeAgent, agent},
	} {
		pages, err := store.ListMemoryPages(src.scope, src.scopeID, userID, "")
		if err != nil {
			continue
		}
		for _, p := range pages {
			if p.Path == "MEMORY.md" {
				continue
			}
			if !isActiveFrontmatter(p.Frontmatter) {
				continue
			}
			candidates = append(candidates, p)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})
	if len(candidates) > cap {
		candidates = candidates[:cap]
	}
	return candidates
}

// isActiveFrontmatter returns true when the frontmatter block carries
// `active: true` (case-insensitive value, whitespace-tolerant). The
// frontmatter is stored as raw text rather than parsed YAML so this
// is a deliberate substring check — keeps things cheap and avoids a
// dependency on a YAML parser at the inject hot path.
func isActiveFrontmatter(fm string) bool {
	if fm == "" {
		return false
	}
	for _, line := range strings.Split(fm, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(strings.ToLower(key)) != "active" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(val), "true") {
			return true
		}
	}
	return false
}

// truncateLines returns s as-is when it's within max lines, otherwise
// keeps the first max lines and appends a one-line truncation marker
// pointing the LLM at memory_read.
func truncateLines(s string, max int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	dropped := len(lines) - max
	return strings.Join(lines[:max], "\n") + fmt.Sprintf("\n…[%d more lines truncated — call memory_read to load the page in full]", dropped)
}
