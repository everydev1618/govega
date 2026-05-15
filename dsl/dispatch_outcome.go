package dsl

import (
	"fmt"
	"strings"
)

// classifyDispatchOutcome decides how a finished dispatched-agent run gets
// filed in the orchestrator's inbox. Pulled out of DispatchToAgent so the
// heuristic is unit-testable in isolation.
//
// Returns subject, body, priority — the three fields the InboxBackend
// needs. Priority is one of "normal" or "urgent".
//
// The earlier classifier had three buckets — error, empty, non-empty —
// and treated every non-empty response as success. That misclassified
// long stream-of-consciousness thrash logs (e.g. an agent fighting a
// sandbox: "no python3... setsid failed... process died... let me try
// nc...") as completed work, which then kicked the dispatch-complete
// poke loop into the orchestrator under a false-success premise.
//
// This pass adds a fourth bucket: "may be incomplete" — urgent, but
// distinct from "task failed" — for responses whose tail carries
// failure signals or trails off mid-thought without a conclusive
// success marker.
func classifyDispatchOutcome(agentName, message, resp string, err error) (subject, body, priority string) {
	if err != nil {
		return fmt.Sprintf("Task failed for %s", agentName),
			fmt.Sprintf("Error: %s\n\nOriginal request: %s", err.Error(), truncateStr(message, 500)),
			"urgent"
	}

	trimmed := strings.TrimSpace(resp)
	if trimmed == "" {
		return fmt.Sprintf("Task incomplete from %s", agentName),
			fmt.Sprintf("Agent produced no output — they may not have the tools required, or they ran without invoking the LLM. Do not assume the task is done; tell the user.\n\nOriginal request: %s", truncateStr(message, 500)),
			"urgent"
	}

	tailLower := strings.ToLower(responseTail(trimmed, dispatchTailWindow))

	// Success markers in the closing window override failure heuristics.
	// Agents commonly narrate failed exploratory steps before pivoting
	// to something that works; the closing summary is what matters.
	for _, m := range dispatchSuccessMarkers {
		if strings.Contains(tailLower, m) {
			return fmt.Sprintf("Task completed by %s", agentName),
				fmt.Sprintf("Result: %s\n\nOriginal request: %s", truncateStr(trimmed, 1000), truncateStr(message, 500)),
				"normal"
		}
	}

	for _, p := range dispatchFailurePhrases {
		if strings.Contains(tailLower, p) {
			return fmt.Sprintf("Task may be incomplete from %s", agentName),
				fmt.Sprintf("Agent's response contains failure or stuck-investigation signals near the end — verify before claiming the task is done.\n\nResponse: %s\n\nOriginal request: %s", truncateStr(trimmed, 1000), truncateStr(message, 500)),
				"urgent"
		}
	}

	if endsMidThought(trimmed) {
		return fmt.Sprintf("Task may be incomplete from %s", agentName),
			fmt.Sprintf("Agent's response trails off without a concluding sentence — verify before claiming the task is done.\n\nResponse: %s\n\nOriginal request: %s", truncateStr(trimmed, 1000), truncateStr(message, 500)),
			"urgent"
	}

	return fmt.Sprintf("Task completed by %s", agentName),
		fmt.Sprintf("Result: %s\n\nOriginal request: %s", truncateStr(trimmed, 1000), truncateStr(message, 500)),
		"normal"
}

const dispatchTailWindow = 600

// dispatchFailurePhrases match (case-insensitive) against the closing
// window of the response. Each phrase is a lifecycle-outcome signal —
// "the work itself failed" — not merely "tried something." Adding
// generic exploratory phrases like "let me try" would false-positive
// every agent that narrates its tool use.
var dispatchFailurePhrases = []string{
	"process died",
	"died immediately",
	"doesn't work",
	"didn't work",
	"still failing",
	"still failed",
	"kept failing",
	"is not available",
	"isn't available",
	"no python",
	"not found",
	"i'm stuck",
	"im stuck",
	"stuck on",
	"unable to",
	"couldn't",
	"can't be",
}

// dispatchSuccessMarkers indicate the agent self-classified as done.
// Their presence in the closing window short-circuits the failure scan,
// since agents often narrate failed attempts before landing on what
// works.
var dispatchSuccessMarkers = []string{
	"✅",
	"200 ok",
	"returns 200",
	"dashboard live",
	"live at http",
}

// insertDispatchOutcome posts an auto-classified dispatch outcome to the
// inbox, deduping against existing pending items with the same
// (from_agent, subject). The classifier produces a small set of
// deterministic subjects ("Task completed by X", "Task may be incomplete
// from X", "Task failed for X", "Task incomplete from X") — when an
// agent is re-dispatched and keeps reproducing the same outcome,
// without this guard the orchestrator's inbox piles up with identical
// urgent cards (observed: synkedup-cto-assistant trailing off
// mid-thought 3+ times in a row).
//
// Returns the inbox id (existing or new) and whether a new row was
// written. Lookup failures are swallowed in favor of inserting — losing
// a completion notification is worse than allowing a dup.
//
// Only DispatchToAgent should call this; agent-driven posts via
// ask_orchestrator/post_to_inbox stay on InsertInboxItem directly so
// agents can repeat themselves intentionally.
func insertDispatchOutcome(backend InboxBackend, fromAgent, subject, body, priority string) (int64, bool, error) {
	const dedupeScanLimit = 200
	existing, err := backend.ListInboxItems("pending", dedupeScanLimit)
	if err == nil {
		for _, it := range existing {
			if it.FromAgent == fromAgent && it.Subject == subject {
				return it.ID, false, nil
			}
		}
	}
	id, err := backend.InsertInboxItem(fromAgent, subject, body, priority)
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// responseTail returns the last `max` characters of s, or all of s if
// shorter. Used to focus the classifier on the agent's closing summary
// rather than the full tool-call narrative.
func responseTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

// endsMidThought reports whether s ends without a conclusive
// terminator — period, question mark, exclamation, close-bracket,
// quote, or terminal emoji-style char. LLMs trained on instructions
// usually wrap up with a period; a response ending on a preposition
// or mid-word usually means the model produced text after its last
// tool result and got cut off before concluding.
func endsMidThought(s string) bool {
	s = strings.TrimRight(s, " \t\n\r")
	if s == "" {
		return false
	}
	last := s[len(s)-1]
	switch last {
	case '.', '!', '?', ')', ']', '}', '"', '\'', '>':
		return false
	}
	return true
}
