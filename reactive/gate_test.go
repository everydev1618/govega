package reactive

import (
	"testing"

	"github.com/everydev1618/govega/events"
)

func TestMatchType(t *testing.T) {
	cases := []struct {
		pattern, typ string
		want         bool
	}{
		{"agent.completed", "agent.completed", true},
		{"agent.completed", "agent.said", false},
		{"agent.*", "agent.completed", true},
		{"agent.*", "agent.said", true},
		{"agent.*", "schedule.fired", false},
		{"*", "anything.at.all", true},
		{"schedule.fired", "agent.completed", false},
	}
	for _, c := range cases {
		if got := matchType(c.pattern, c.typ); got != c.want {
			t.Errorf("matchType(%q, %q) = %v, want %v", c.pattern, c.typ, got, c.want)
		}
	}
}

func TestEvalWhere(t *testing.T) {
	data := map[string]any{
		"status": "failed",
		"error":  "connection refused by upstream",
		"count":  3,
	}
	cases := []struct {
		where   string
		want    bool
		wantErr bool
	}{
		{"", true, false},                              // empty => always pass
		{"status == failed", true, false},              //
		{"status == ok", false, false},                 //
		{"status != ok", true, false},                  //
		{"status != failed", false, false},             //
		{`status == "failed"`, true, false},            // quoted RHS
		{"error contains refused", true, false},        //
		{"error contains success", false, false},       //
		{"error ~= upstream", true, false},             // ~= is substring
		{"count == 3", true, false},                    // non-string coerced
		{"missing == whatever", false, false},          // missing key, != empty
		{"missing != whatever", true, false},           // missing key
		{"garbage clause with no operator", false, true}, // malformed
	}
	for _, c := range cases {
		got, err := evalWhere(c.where, data)
		if c.wantErr {
			if err == nil {
				t.Errorf("evalWhere(%q) expected error, got nil", c.where)
			}
			continue
		}
		if err != nil {
			t.Errorf("evalWhere(%q) unexpected error: %v", c.where, err)
			continue
		}
		if got != c.want {
			t.Errorf("evalWhere(%q) = %v, want %v", c.where, got, c.want)
		}
	}
}

func TestPassRules(t *testing.T) {
	e := events.Event{Type: "agent.completed", Data: map[string]any{"status": "failed"}}

	// Type matches and where matches => pass.
	if ok, err := passRules("agent.*", "status == failed", e); err != nil || !ok {
		t.Fatalf("expected pass, got ok=%v err=%v", ok, err)
	}
	// Type matches but where rejects => no pass, no error.
	if ok, err := passRules("agent.*", "status == ok", e); err != nil || ok {
		t.Fatalf("expected reject, got ok=%v err=%v", ok, err)
	}
	// Type does not match => no pass, and where is never evaluated.
	if ok, err := passRules("schedule.*", "garbage", e); err != nil || ok {
		t.Fatalf("type mismatch must short-circuit before where; got ok=%v err=%v", ok, err)
	}
}
