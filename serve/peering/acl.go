package peering

import (
	"fmt"
	"strings"
	"time"
)

// AuthorizeDecision is the result of an authorization check. Callers act on
// OK; on denial they write an audit row with status=denied + DenialReason
// and reject the operation.
//
// Grant is populated only when OK=true; it carries the per-op token cap so
// the streaming layer can enforce it without a second store lookup.
type AuthorizeDecision struct {
	OK           bool
	DenialReason string
	Grant        *Grant
}

// rateLimitWindow is the window over which max_ops_per_hour is enforced.
// Lifted to a const so tests can reason about it; not configurable today.
const rateLimitWindow = time.Hour

// Authorize is the single decision point for whether a remote peer's
// invocation should proceed against a local agent. It returns OK + the
// matched grant, or a denial reason in priority order:
//
//  1. unknown peer (no row in peer_orchestrators)
//  2. peer paused (trust_level = paused)
//  3. no grant (no row in peer_agent_grants)
//  4. grant inactive (active = false)
//  5. rate limit (>= MaxOpsPerHour non-denied ops in the last hour)
//
// The agent name is canonicalized via CanonicalizeAgent before grant lookup
// so 'Researcher:12345' resolves to a grant stored as 'researcher'.
func Authorize(s Store, peerNodeID, agent string, now time.Time) (AuthorizeDecision, error) {
	peer, err := s.GetPeer(peerNodeID)
	if err != nil {
		return AuthorizeDecision{}, fmt.Errorf("lookup peer: %w", err)
	}
	if peer == nil {
		return AuthorizeDecision{DenialReason: "unknown peer"}, nil
	}
	if peer.TrustLevel == TrustPaused {
		return AuthorizeDecision{DenialReason: "peer paused"}, nil
	}

	canonical := CanonicalizeAgent(agent)
	if canonical == "" {
		return AuthorizeDecision{DenialReason: "empty agent name"}, nil
	}

	grant, err := s.GetGrant(peerNodeID, canonical)
	if err != nil {
		return AuthorizeDecision{}, fmt.Errorf("lookup grant: %w", err)
	}
	if grant == nil {
		return AuthorizeDecision{DenialReason: "no grant for agent"}, nil
	}
	if !grant.Active {
		return AuthorizeDecision{DenialReason: "grant inactive"}, nil
	}

	since := now.Add(-rateLimitWindow)
	count, err := s.CountOpsInWindow(peerNodeID, canonical, since)
	if err != nil {
		return AuthorizeDecision{}, fmt.Errorf("rate-limit lookup: %w", err)
	}
	if count >= grant.MaxOpsPerHour {
		return AuthorizeDecision{
			DenialReason: fmt.Sprintf("rate limit exceeded: %d/%d in last hour", count, grant.MaxOpsPerHour),
		}, nil
	}

	return AuthorizeDecision{OK: true, Grant: grant}, nil
}

// CanonicalizeAgent normalizes an agent name to the same form the memory
// layer uses (see serve/memory_tools.go): lowercase, with any trailing
// ":pid" suffix stripped. Returns "" for inputs that contain no name part
// (e.g. ":pid-only").
func CanonicalizeAgent(s string) string {
	s = strings.ToLower(s)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return s
}
