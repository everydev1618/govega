package events

import (
	"sync"
	"testing"
	"time"
)

func TestPublishStampsIDAndTime(t *testing.T) {
	b := NewBus()
	defer b.Close()

	got := b.Publish(Event{Type: "agent.completed"})
	if got.ID == "" {
		t.Fatal("Publish should stamp a non-empty ID when none is provided")
	}
	if got.Time.IsZero() {
		t.Fatal("Publish should stamp Time when zero")
	}
}

func TestPublishPreservesProvidedID(t *testing.T) {
	b := NewBus()
	defer b.Close()

	got := b.Publish(Event{ID: "fixed-id", Type: "agent.completed"})
	if got.ID != "fixed-id" {
		t.Fatalf("Publish must preserve a provided ID, got %q", got.ID)
	}
}

func TestUniqueIDs(t *testing.T) {
	b := NewBus()
	defer b.Close()

	seen := map[string]bool{}
	for range 1000 {
		e := b.Publish(Event{Type: "x"})
		if seen[e.ID] {
			t.Fatalf("duplicate ID generated: %q", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestDurableReceivesAllEventsInOrder(t *testing.T) {
	b := NewBus()
	defer b.Close()

	sub := b.Subscribe(Durable, nil)
	defer sub.Close()

	const n = 100
	go func() {
		for i := range n {
			b.Publish(Event{Type: "seq", Data: map[string]any{"i": i}})
		}
	}()

	for want := range n {
		select {
		case e := <-sub.C:
			if got := e.Data["i"].(int); got != want {
				t.Fatalf("durable delivery out of order: want %d got %d", want, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("durable subscriber missed event %d (dropped or blocked)", want)
		}
	}
}

// A lossy subscriber that never reads must not block the publisher, and must
// not starve a durable subscriber, which must still receive every event.
func TestLossyDoesNotBlockPublisherOrStarveDurable(t *testing.T) {
	b := NewBus()
	defer b.Close()

	lossy := b.Subscribe(Lossy, nil) // intentionally never drained
	defer lossy.Close()
	durable := b.Subscribe(Durable, nil)
	defer durable.Close()

	const n = 1000
	done := make(chan struct{})
	go func() {
		for i := range n {
			b.Publish(Event{Type: "flood", Data: map[string]any{"i": i}})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked on a full lossy subscriber")
	}

	for want := range n {
		select {
		case e := <-durable.C:
			if got := e.Data["i"].(int); got != want {
				t.Fatalf("durable out of order under load: want %d got %d", want, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("durable subscriber lost event %d under lossy backpressure", want)
		}
	}
}

func TestFilterOnlyMatching(t *testing.T) {
	b := NewBus()
	defer b.Close()

	sub := b.Subscribe(Durable, func(e Event) bool { return e.Type == "keep" })
	defer sub.Close()

	b.Publish(Event{Type: "drop"})
	b.Publish(Event{Type: "keep", Data: map[string]any{"v": 1}})
	b.Publish(Event{Type: "drop"})

	select {
	case e := <-sub.C:
		if e.Type != "keep" {
			t.Fatalf("filter let through %q", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("filtered subscriber never received the matching event")
	}

	select {
	case e := <-sub.C:
		t.Fatalf("filter should have dropped everything else, got %q", e.Type)
	case <-time.After(100 * time.Millisecond):
		// expected: no more events
	}
}

func TestCloseClosesChannel(t *testing.T) {
	b := NewBus()
	defer b.Close()

	sub := b.Subscribe(Durable, nil)
	sub.Close()

	select {
	case _, ok := <-sub.C:
		if ok {
			t.Fatal("channel should be closed (or empty then closed) after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after Close")
	}

	// Publishing after a subscriber closes must not panic.
	b.Publish(Event{Type: "after-close"})
}

// Concurrent publishers and a durable subscriber: no races, all delivered.
func TestConcurrentPublishersDurable(t *testing.T) {
	b := NewBus()
	defer b.Close()

	sub := b.Subscribe(Durable, nil)
	defer sub.Close()

	const writers, each = 8, 50
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range each {
				b.Publish(Event{Type: "c"})
			}
		})
	}

	got := 0
	deadline := time.After(3 * time.Second)
	for got < writers*each {
		select {
		case <-sub.C:
			got++
		case <-deadline:
			t.Fatalf("only received %d/%d under concurrent publish", got, writers*each)
		}
	}
	wg.Wait()
}
