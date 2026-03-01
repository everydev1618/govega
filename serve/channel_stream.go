package serve

import (
	"sync"
)

// channelStreamSubscriber is a single SSE client subscribed to a channel stream.
type channelStreamSubscriber struct {
	ch     chan ChannelEvent
	closed bool
}

// channelStream tracks a server-side channel stream that runs independently of
// any connected SSE client. Events are buffered in history so reconnecting
// clients can replay them.
type channelStream struct {
	channelName string
	done        chan struct{}

	mu          sync.Mutex
	history     []ChannelEvent
	subscribers []*channelStreamSubscriber
	finished    bool
}

// publish sends an event to all active subscribers and appends it to history.
func (cs *channelStream) publish(event ChannelEvent) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.history = append(cs.history, event)
	for _, sub := range cs.subscribers {
		if !sub.closed {
			select {
			case sub.ch <- event:
			default: // subscriber too slow, skip
			}
		}
	}
}

// subscribe returns a snapshot of all past events plus a channel for future events.
func (cs *channelStream) subscribe() ([]ChannelEvent, chan ChannelEvent) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	snapshot := make([]ChannelEvent, len(cs.history))
	copy(snapshot, cs.history)
	ch := make(chan ChannelEvent, 256)
	cs.subscribers = append(cs.subscribers, &channelStreamSubscriber{ch: ch})
	return snapshot, ch
}

// unsubscribe removes a subscriber channel.
func (cs *channelStream) unsubscribe(ch chan ChannelEvent) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, sub := range cs.subscribers {
		if sub.ch == ch {
			sub.closed = true
			return
		}
	}
}

// finish closes all subscriber channels. Called when the stream completes.
func (cs *channelStream) finish() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.finished = true
	for _, sub := range cs.subscribers {
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
	}
}
