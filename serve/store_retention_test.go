package serve

import (
	"testing"
	"time"
)

// backdateChatMessages ages every chat message for the given agent, since
// InsertChatMessage always stamps CURRENT_TIMESTAMP.
func backdateChatMessages(t *testing.T, store Store, agent string, age time.Duration) {
	t.Helper()
	cutoff := time.Now().UTC().Add(-age)
	switch s := store.(type) {
	case *SQLiteStore:
		if _, err := s.db.Exec(`UPDATE chat_messages SET created_at = ? WHERE agent = ?`, cutoff, agent); err != nil {
			t.Fatalf("backdate chat: %v", err)
		}
	case *PostgresStore:
		if _, err := s.db.Exec(`UPDATE chat_messages SET created_at = $1 WHERE agent = $2`, cutoff, agent); err != nil {
			t.Fatalf("backdate chat: %v", err)
		}
	default:
		t.Fatalf("unknown store type %T", store)
	}
}

// TestSweepRetention verifies old rows are deleted and recent rows kept,
// per-table, on both backends.
func TestSweepRetention(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		now := time.Now().UTC()
		old := now.Add(-40 * 24 * time.Hour)

		// Events: one old, one fresh.
		mustInsert := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
		mustInsert(store.InsertEvent(StoreEvent{Type: "old", ProcessID: "p1", Timestamp: old}))
		mustInsert(store.InsertEvent(StoreEvent{Type: "fresh", ProcessID: "p2", Timestamp: now}))

		// Snapshots: one old, one fresh.
		mustInsert(store.InsertProcessSnapshot(ProcessSnapshot{ProcessID: "p1", StartedAt: old, SnapshotAt: old}))
		mustInsert(store.InsertProcessSnapshot(ProcessSnapshot{ProcessID: "p2", StartedAt: now, SnapshotAt: now}))

		// Chat: two messages for an "old" agent (backdated) + one fresh.
		mustInsert(store.InsertChatMessage("stale-agent", "user", "old message", nil))
		mustInsert(store.InsertChatMessage("stale-agent", "assistant", "old reply", nil))
		backdateChatMessages(t, store, "stale-agent", 40*24*time.Hour)
		mustInsert(store.InsertChatMessage("live-agent", "user", "fresh message", nil))

		res, err := store.SweepRetention(RetentionPolicy{
			Events:       30 * 24 * time.Hour,
			Snapshots:    30 * 24 * time.Hour,
			ChatMessages: 30 * 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("SweepRetention: %v", err)
		}

		if res.Events != 1 {
			t.Errorf("swept %d events, want 1", res.Events)
		}
		if res.Snapshots != 1 {
			t.Errorf("swept %d snapshots, want 1", res.Snapshots)
		}
		if res.ChatMessages != 2 {
			t.Errorf("swept %d chat messages, want 2", res.ChatMessages)
		}

		// Fresh rows survive.
		events, err := store.ListEvents(10)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 1 || events[0].Type != "fresh" {
			t.Errorf("surviving events = %+v, want only the fresh one", events)
		}
		msgs, err := store.ListChatMessages("live-agent")
		if err != nil {
			t.Fatalf("ListChatMessages: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("live-agent messages = %d, want 1", len(msgs))
		}
	})
}

// TestSweepRetentionZeroDisables verifies a zero duration means "keep
// forever" for that table.
func TestSweepRetentionZeroDisables(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		old := time.Now().UTC().Add(-400 * 24 * time.Hour)
		if err := store.InsertEvent(StoreEvent{Type: "ancient", ProcessID: "p1", Timestamp: old}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := store.InsertChatMessage("keeper", "user", "ancient chat", nil); err != nil {
			t.Fatalf("insert chat: %v", err)
		}
		backdateChatMessages(t, store, "keeper", 400*24*time.Hour)

		res, err := store.SweepRetention(RetentionPolicy{}) // all zero
		if err != nil {
			t.Fatalf("SweepRetention: %v", err)
		}
		if res.Events != 0 || res.Snapshots != 0 || res.ChatMessages != 0 {
			t.Errorf("zero policy swept rows: %+v", res)
		}

		events, _ := store.ListEvents(10)
		if len(events) != 1 {
			t.Errorf("ancient event was deleted despite disabled retention")
		}
	})
}
