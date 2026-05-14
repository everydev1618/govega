package serve

import (
	"regexp"
	"strings"
)

// Refs govega#71.

// wikiLinkRe matches `[[path]]` wikilinks. Captures the path.
var wikiLinkRe = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)

// mdLinkRe matches `[label](dest)` markdown links. Captures the dest.
var mdLinkRe = regexp.MustCompile(`\[[^\]\n]*\]\(([^)\n]+)\)`)

// extractMemoryLinks finds every memory-page link in `content` and
// returns the destinations in document order, deduped. Catches both
// wikilinks (`[[topics/sushi.md]]`) and markdown links to memory
// paths (`[sushi](topics/sushi.md)`). External URLs (http(s)://,
// mailto:, etc.) are skipped — only intra-wiki references make it
// into memory_links.
func extractMemoryLinks(content string) []string {
	seen := make(map[string]struct{})
	out := []string{}

	addIfLocal := func(raw string) {
		p := strings.TrimSpace(raw)
		if p == "" {
			return
		}
		// Trim trailing punctuation that markdown sometimes leaves attached.
		p = strings.TrimRight(p, ".,;:!?")
		// External / non-memory destinations.
		if strings.Contains(p, "://") || strings.HasPrefix(p, "mailto:") ||
			strings.HasPrefix(p, "tel:") || strings.HasPrefix(p, "#") {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}

	for _, m := range wikiLinkRe.FindAllStringSubmatch(content, -1) {
		addIfLocal(m[1])
	}
	for _, m := range mdLinkRe.FindAllStringSubmatch(content, -1) {
		addIfLocal(m[1])
	}
	return out
}

// resolveScope normalizes the optional "scope" tool param. Empty
// defaults to the shared user wiki. Returns (scope, scopeID) where
// scopeID is the userID for user scope or the agent name for agent
// scope.
func resolveScope(rawScope, userID, agent string) (MemoryScope, string, error) {
	switch strings.ToLower(strings.TrimSpace(rawScope)) {
	case "", "user", "shared":
		return MemoryScopeUser, userID, nil
	case "agent", "private":
		return MemoryScopeAgent, agent, nil
	default:
		return "", "", &scopeError{raw: rawScope}
	}
}

type scopeError struct{ raw string }

func (e *scopeError) Error() string {
	return "invalid scope " + e.raw + ` — must be "user" (shared, default) or "agent" (private)`
}

// previewForSearch returns a short window of content around the first
// case-insensitive occurrence of `query`, padded out to ~200 chars.
// Used by memory_search to give the LLM enough context to decide
// whether to read the full page.
func previewForSearch(content, query string) string {
	const window = 200
	if content == "" {
		return ""
	}
	lc := strings.ToLower(content)
	q := strings.ToLower(query)
	idx := strings.Index(lc, q)
	if idx < 0 {
		// Match was on path, not content — return the head.
		if len(content) <= window {
			return content
		}
		return content[:window] + "…"
	}
	start := idx - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(content) {
		end = len(content)
	}
	out := content[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(content) {
		out = out + "…"
	}
	return out
}
