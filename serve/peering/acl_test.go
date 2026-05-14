package peering

import (
	"strings"
	"testing"
	"time"
)

// authorize is invoked through the package-level function so tests double as
// usage docs. Each test sets up a fresh in-memory store, populates the state
// it cares about, and asserts on the decision.

func TestAuthorize_UnknownPeer(t *testing.T) {
	s := newTestStore(t)
	d, err := Authorize(s, "vega:nobody", "researcher", time.Now().UTC())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if d.OK {
		t.Fatal("unknown peer should be denied")
	}
	if !strings.Contains(d.DenialReason, "unknown") {
		t.Fatalf("denial reason should mention unknown peer; got %q", d.DenialReason)
	}
}

func TestAuthorize_PausedPeer(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	p.TrustLevel = TrustPaused
	_ = s.UpsertPeer(p)
	_ = s.UpsertGrant(sampleGrant(p.NodeID))

	d, _ := Authorize(s, p.NodeID, "researcher", time.Now().UTC())
	if d.OK {
		t.Fatal("paused peer should be denied even with a grant")
	}
	if !strings.Contains(d.DenialReason, "paused") {
		t.Fatalf("denial reason should mention paused; got %q", d.DenialReason)
	}
}

func TestAuthorize_NoGrant(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)

	d, _ := Authorize(s, p.NodeID, "researcher", time.Now().UTC())
	if d.OK {
		t.Fatal("default-deny: no grant means no access")
	}
	if !strings.Contains(d.DenialReason, "grant") {
		t.Fatalf("denial reason should mention grant; got %q", d.DenialReason)
	}
}

func TestAuthorize_InactiveGrant(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)
	g := sampleGrant(p.NodeID)
	g.Active = false
	_ = s.UpsertGrant(g)

	d, _ := Authorize(s, p.NodeID, "researcher", time.Now().UTC())
	if d.OK {
		t.Fatal("inactive grant should deny")
	}
	if !strings.Contains(d.DenialReason, "inactive") {
		t.Fatalf("denial reason should mention inactive; got %q", d.DenialReason)
	}
}

func TestAuthorize_HappyPath(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)
	_ = s.UpsertGrant(sampleGrant(p.NodeID))

	d, err := Authorize(s, p.NodeID, "researcher", time.Now().UTC())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !d.OK {
		t.Fatalf("expected OK; denial = %q", d.DenialReason)
	}
	if d.Grant == nil {
		t.Fatal("OK decision should carry the matched Grant")
	}
	if d.Grant.MaxTokensPerOp != 8000 || d.Grant.MaxOpsPerHour != 30 {
		t.Fatalf("Grant limits not surfaced: %+v", d.Grant)
	}
}

func TestAuthorize_RateLimitExceeded(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)
	g := sampleGrant(p.NodeID)
	g.MaxOpsPerHour = 2
	_ = s.UpsertGrant(g)

	now := time.Now().UTC()
	// Two successful ops in the last hour exhausts the quota.
	for i := range 2 {
		e := sampleAudit()
		e.PeerNodeID = p.NodeID
		e.Agent = g.LocalAgent
		e.Timestamp = now.Add(-time.Duration(i+1) * time.Minute)
		e.Status = AuditStatusOK
		if _, err := s.InsertAudit(e); err != nil {
			t.Fatal(err)
		}
	}

	d, _ := Authorize(s, p.NodeID, g.LocalAgent, now)
	if d.OK {
		t.Fatal("rate limit should have triggered")
	}
	if !strings.Contains(d.DenialReason, "rate") {
		t.Fatalf("denial reason should mention rate; got %q", d.DenialReason)
	}
}

func TestAuthorize_OldOpsDontCount(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)
	g := sampleGrant(p.NodeID)
	g.MaxOpsPerHour = 1
	_ = s.UpsertGrant(g)

	now := time.Now().UTC()
	// An op from 2 hours ago should not block a new one.
	e := sampleAudit()
	e.PeerNodeID = p.NodeID
	e.Agent = g.LocalAgent
	e.Timestamp = now.Add(-2 * time.Hour)
	e.Status = AuditStatusOK
	_, _ = s.InsertAudit(e)

	d, _ := Authorize(s, p.NodeID, g.LocalAgent, now)
	if !d.OK {
		t.Fatalf("ops older than the window must not count; denial = %q", d.DenialReason)
	}
}

func TestAuthorize_AgentNameCanonicalized(t *testing.T) {
	// Vega's memory layer canonicalizes agent names (lowercase, strip
	// :pid suffix; see serve/memory_tools.go). Peering grants live in
	// the same namespace — so a grant stored as 'researcher' must match
	// an inbound Invoke that names the agent 'Researcher:12345'.
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)
	g := sampleGrant(p.NodeID)
	g.LocalAgent = "researcher"
	_ = s.UpsertGrant(g)

	d, _ := Authorize(s, p.NodeID, "Researcher:12345", time.Now().UTC())
	if !d.OK {
		t.Fatalf("agent canonicalization broken: denial = %q", d.DenialReason)
	}
}

func TestCanonicalizeAgent(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Researcher", "researcher"},
		{"researcher", "researcher"},
		{"Researcher:12345", "researcher"},
		{"WRITER:proc-abc-def", "writer"},
		{"", ""},
		{":pid-only", ""},
	}
	for _, tt := range tests {
		if got := CanonicalizeAgent(tt.in); got != tt.want {
			t.Errorf("CanonicalizeAgent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
