package dsl

import (
	"errors"
	"testing"
	"time"
)

// fakeInboxBackend is a minimal InboxBackend for testing insertDispatchOutcome.
type fakeInboxBackend struct {
	items     []InboxItem
	nextID    int64
	insertErr error
	listErr   error
}

func (f *fakeInboxBackend) InsertInboxItem(fromAgent, subject, body, priority string) (int64, error) {
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	f.nextID++
	f.items = append(f.items, InboxItem{
		ID:        f.nextID,
		FromAgent: fromAgent,
		Subject:   subject,
		Body:      body,
		Priority:  priority,
		Status:    "pending",
		CreatedAt: time.Now(),
	})
	return f.nextID, nil
}

func (f *fakeInboxBackend) ListInboxItems(status string, limit int) ([]InboxItem, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]InboxItem, 0, len(f.items))
	for _, it := range f.items {
		if status == "" || status == "all" || it.Status == status {
			out = append(out, it)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeInboxBackend) ResolveInboxItem(id int64, resolution string) error {
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].Status = "resolved"
			f.items[i].Resolution = resolution
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeInboxBackend) DeleteInboxItem(id int64) error {
	for i := range f.items {
		if f.items[i].ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

// TestInsertDispatchOutcomeDedup pins the dedupe behavior used by
// DispatchToAgent: when an agent finishes a dispatched run, the
// classifier-generated subject is deterministic ("Task may be incomplete
// from <agent>", "Task completed by <agent>", etc.). Re-dispatching the
// same agent with the same outcome must NOT produce a second pending
// inbox item — the orchestrator was getting buried under identical urgent
// cards from agents that kept trailing off mid-thought.
func TestInsertDispatchOutcomeDedup(t *testing.T) {
	t.Run("first insert goes through", func(t *testing.T) {
		b := &fakeInboxBackend{}
		id, inserted, err := insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "trail-off body", "urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !inserted {
			t.Errorf("first insert should report inserted=true")
		}
		if id == 0 {
			t.Errorf("expected non-zero id")
		}
		if len(b.items) != 1 {
			t.Errorf("expected 1 stored item, got %d", len(b.items))
		}
	})

	t.Run("identical pending dup is skipped", func(t *testing.T) {
		b := &fakeInboxBackend{}
		firstID, _, _ := insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body v1", "urgent")
		secondID, inserted, err := insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body v2", "urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserted {
			t.Errorf("duplicate insert should report inserted=false")
		}
		if secondID != firstID {
			t.Errorf("dedupe should return existing id %d, got %d", firstID, secondID)
		}
		if len(b.items) != 1 {
			t.Errorf("expected 1 stored item after dedupe, got %d", len(b.items))
		}
	})

	t.Run("resolved item with same subject does NOT block a new insert", func(t *testing.T) {
		b := &fakeInboxBackend{}
		firstID, _, _ := insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body v1", "urgent")
		if err := b.ResolveInboxItem(firstID, "handled"); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		_, inserted, err := insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body v2", "urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !inserted {
			t.Errorf("after the prior item was resolved, a fresh insert must go through")
		}
		if len(b.items) != 2 {
			t.Errorf("expected 2 stored items (one resolved, one new), got %d", len(b.items))
		}
	})

	t.Run("different subject for same agent is not a duplicate", func(t *testing.T) {
		b := &fakeInboxBackend{}
		_, _, _ = insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body", "urgent")
		_, inserted, err := insertDispatchOutcome(b, "scout", "Task completed by scout", "body", "normal")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !inserted {
			t.Errorf("different subject should not dedupe")
		}
		if len(b.items) != 2 {
			t.Errorf("expected 2 items, got %d", len(b.items))
		}
	})

	t.Run("different agent for same subject pattern is not a duplicate", func(t *testing.T) {
		b := &fakeInboxBackend{}
		_, _, _ = insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body", "urgent")
		_, inserted, err := insertDispatchOutcome(b, "remy", "Task may be incomplete from remy", "body", "urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !inserted {
			t.Errorf("different agent should not dedupe")
		}
		if len(b.items) != 2 {
			t.Errorf("expected 2 items, got %d", len(b.items))
		}
	})

	t.Run("list error falls through to insert so we never lose a notification", func(t *testing.T) {
		// If the dedupe lookup fails, we prefer inserting (and accepting a
		// possible dup) over silently dropping the completion notification.
		b := &fakeInboxBackend{listErr: errors.New("db down")}
		id, inserted, err := insertDispatchOutcome(b, "scout", "Task may be incomplete from scout", "body", "urgent")
		if err != nil {
			t.Fatalf("expected dedupe failure to be swallowed, got: %v", err)
		}
		if !inserted {
			t.Errorf("on dedupe-lookup error we should still insert")
		}
		if id == 0 {
			t.Errorf("expected non-zero id")
		}
	})
}
