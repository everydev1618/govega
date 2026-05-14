package serve

import (
	"fmt"
	"strings"
)

// wikiInjectionLineLimit caps each injected MEMORY.md at this many
// lines. Anything past the cap is summarized as a truncation marker —
// the LLM follows up with memory_read for the full page.
const wikiInjectionLineLimit = 200

// formatWikiMemoryForInjection loads the shared user wiki's MEMORY.md
// and the calling agent's private MEMORY.md, concatenates them with
// clear headers, and returns the result for injection into the
// system prompt. Returns "" when both pages are absent or empty so
// the prompt doesn't carry dead weight. Refs govega#71.
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

	if b.Len() == 0 {
		return ""
	}
	b.WriteString("\nUse `memory_read` to drill into linked pages. Use `memory_write` / `memory_append` / `memory_edit` to update memory. Keep `MEMORY.md` itself short — it's the index, not the storage.")
	return b.String()
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
