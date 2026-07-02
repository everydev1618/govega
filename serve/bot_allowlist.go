package serve

import "strings"

// newAllowSet builds a lookup set from a list of user IDs, trimming blanks.
// Returns nil for an empty list (which userAllowed treats as open access).
func newAllowSet(ids []string) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			m[id] = true
		}
	}
	return m
}

// userAllowed reports whether a chat bot should process a message from userID.
// An empty allowlist means open access (backward compatible); a non-empty one
// restricts processing to exactly those user IDs, preventing strangers from
// sharing the owner's single agent conversation.
func userAllowed(allowed map[string]bool, userID string) bool {
	if len(allowed) == 0 {
		return true
	}
	return allowed[userID]
}
