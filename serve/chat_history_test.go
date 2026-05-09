package serve

import (
	"testing"
)

// TestListChatMessages_IncludesIDAndTimestamp covers the #48 contract:
// chat history rows expose their stable id and created_at so the frontend
// can stop synthesizing fake "hist_<agent>_<idx>" keys and stop defaulting
// timestamps to epoch-0. The columns already exist in the schema; this
// just exposes them through the response shape.
func TestListChatMessages_IncludesIDAndTimestamp(t *testing.T) {
	store := newTestStore(t)

	if err := store.InsertChatMessage("riley", "user", "first"); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := store.InsertChatMessage("riley", "assistant", "second"); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	msgs, err := store.ListChatMessages("riley")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}

	for i, m := range msgs {
		if m.ID == 0 {
			t.Errorf("msg %d: id = 0, want non-zero", i)
		}
		if m.CreatedAt.IsZero() {
			t.Errorf("msg %d: created_at zero", i)
		}
	}
	// IDs are autoincrementing, so the second message should have a higher id.
	if msgs[1].ID <= msgs[0].ID {
		t.Errorf("ids out of order: %d, %d", msgs[0].ID, msgs[1].ID)
	}
	// Content/role still round-trip.
	if msgs[0].Role != "user" || msgs[0].Content != "first" {
		t.Errorf("msg 0 wrong: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "second" {
		t.Errorf("msg 1 wrong: %+v", msgs[1])
	}
}
