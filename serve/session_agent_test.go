package serve

import "testing"

// resolveSessionName decides, from a chat URL's {name}, whether a request is a
// per-session conversation. For an agent in Config.SessionedAgents a
// "base:session" name is preserved (isolated process + chat thread); for every
// other agent a ":suffix" collapses to the base, so non-sessioned traffic is
// byte-for-byte unchanged from the legacy behavior.
func TestResolveSessionName(t *testing.T) {
	sessioned := map[string]bool{"ea": true}
	cases := []struct {
		raw, wantName, wantBase string
		wantSession             bool
	}{
		{"ea", "ea", "ea", false},                               // no suffix
		{"ea:stacie", "ea:stacie", "ea", true},                  // sessioned agent → preserved
		{"ea:", "ea", "ea", false},                              // empty session → collapse
		{"orchestrator", "orchestrator", "orchestrator", false}, // plain agent
		{"guide:user123", "guide", "guide", false},              // not sessioned → legacy collapse
	}
	for _, c := range cases {
		name, base, isSession := resolveSessionName(c.raw, sessioned)
		if name != c.wantName || base != c.wantBase || isSession != c.wantSession {
			t.Errorf("resolveSessionName(%q) = (%q,%q,%v); want (%q,%q,%v)",
				c.raw, name, base, isSession, c.wantName, c.wantBase, c.wantSession)
		}
	}

	// A nil sessioned map disables sessioning entirely — everything collapses.
	if name, base, isSession := resolveSessionName("ea:stacie", nil); name != "ea" || base != "ea" || isSession {
		t.Errorf("nil sessioned map must collapse; got (%q,%q,%v)", name, base, isSession)
	}
}
