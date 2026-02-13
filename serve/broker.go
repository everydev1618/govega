package serve

import (
	"sync"
)

const maxSubscribers = 50

// EventBroker fans out events to SSE subscribers.
type EventBroker struct {
	subscribers map[chan BrokerEvent]struct{}
	mu          sync.RWMutex
}

// NewEventBroker creates a new broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		subscribers: make(map[chan BrokerEvent]struct{}),
	}
}

// Subscribe returns a channel that receives events.
// The caller must call Unsubscribe when done.
func (b *EventBroker) Subscribe() chan BrokerEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.subscribers) >= maxSubscribers {
		return nil
	}

	ch := make(chan BrokerEvent, 64)
	b.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber channel.
func (b *EventBroker) Unsubscribe(ch chan BrokerEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
}

// Close closes all subscriber channels, causing SSE handlers to exit.
func (b *EventBroker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subscribers {
		close(ch)
		delete(b.subscribers, ch)
	}
}

// Publish sends an event to all subscribers.
// Non-blocking: if a subscriber's buffer is full, the event is dropped for that subscriber.
func (b *EventBroker) Publish(event BrokerEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber too slow, drop event
		}
	}
}
