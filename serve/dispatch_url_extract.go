package serve

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// extractURLs returns the unique http/https URLs found in s, in the order
// they first appear. We hand these to the orchestrator's dispatch-complete
// poke as a separate "verbatim" block: LLMs reliably lose precision on
// long opaque hex suffixes (e.g. `acme-dashboard-8b24eb` → made-up
// `nadia-dashboard-zw5gpf`), so we don't trust the orchestrator's LLM to
// retype them from the dispatched agent's response.
//
// Trailing punctuation (`. , ) ] " '`) is stripped — URLs at sentence
// boundaries shouldn't drag the terminal punctuation into the captured
// link.
var urlRegex = regexp.MustCompile(`https?://[^\s<>"'\[\]{}]+`)

func extractURLs(s string) []string {
	matches := urlRegex.FindAllString(s, -1)
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		m = strings.TrimRight(m, ".,)]'\"")
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] }) // deterministic for testability; first-seen order isn't worth preserving here
	return out
}

// composeDispatchCompletePoke returns the message the orchestrator is
// woken up with after a dispatched agent finishes. The agent's URLs are
// pulled out separately and labelled "verbatim" so the orchestrator
// echoes them character-for-character rather than reproducing the
// shape of the hex suffix from memory.
func composeDispatchCompletePoke(completedAgent, dispatchResp string) string {
	base := fmt.Sprintf("Agent **%s** just finished a task. Check your inbox (list_inbox) for their report and take action — resolve it, dispatch follow-up work, or escalate if needed. Do NOT just acknowledge — act on the results.", completedAgent)

	urls := extractURLs(dispatchResp)
	if len(urls) == 0 {
		return base
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	if len(urls) == 1 {
		b.WriteString("URL the agent produced — when relaying to the user, paste these exact characters, do NOT retype or summarize:\n  ")
		b.WriteString(urls[0])
	} else {
		b.WriteString("URLs the agent produced — when relaying to the user, paste these exact characters, do NOT retype or summarize:\n")
		for _, u := range urls {
			b.WriteString("  ")
			b.WriteString(u)
			b.WriteString("\n")
		}
	}
	return b.String()
}
