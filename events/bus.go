package events

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DeliveryMode controls how a subscriber tolerates backpressure.
type DeliveryMode int

const (
	// Lossy drops events when the subscriber's buffer is full rather than
	// blocking the publisher. Correct for a UI that can miss a frame.
	Lossy DeliveryMode = iota
	// Durable never drops: events queue without bound until delivered, in
	// order. Correct for reactive triggers, which must not miss a creak.
	Durable
)

// lossyBuffer is the fixed channel depth for lossy subscribers before drops.
const lossyBuffer = 64

// Bus is an in-process publish/subscribe spine. Publish is non-blocking with
// respect to every subscriber regardless of delivery mode.
//
// Durability here means guaranteed in-process, in-order delivery to durable
// subscribers. Cross-restart persistence (an events table with replay) is a
// later layer behind this same interface — see design §8 open decision #1.
type Bus struct {
	mu     sync.RWMutex
	subs   map[*Subscription]struct{}
	nextID atomic.Uint64
	closed bool
}

// NewBus returns a ready-to-use Bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[*Subscription]struct{})}
}

// Subscribe registers a subscriber. If filter is non-nil, only events for
// which it returns true are delivered. Read from the returned Subscription's C
// channel; call Close when done.
func (b *Bus) Subscribe(mode DeliveryMode, filter func(Event) bool) *Subscription {
	out := make(chan Event, lossyBuffer)
	sub := &Subscription{
		C:      out,
		out:    out,
		mode:   mode,
		filter: filter,
		bus:    b,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	if mode == Durable {
		go sub.pump()
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

// Publish stamps the event's ID and Time if unset, delivers it to every
// matching subscriber, and returns the stamped event.
func (b *Bus) Publish(e Event) Event {
	if e.ID == "" {
		e.ID = "evt-" + strconv.FormatUint(b.nextID.Add(1), 10)
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}

	b.mu.RLock()
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.RUnlock()

	for _, s := range subs {
		if s.filter != nil && !s.filter(e) {
			continue
		}
		s.deliver(e)
	}
	return e
}

// Close tears down the bus and all its subscriptions.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}

func (b *Bus) remove(s *Subscription) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// Subscription is a handle to a stream of events. Read from C.
type Subscription struct {
	C <-chan Event

	out    chan Event
	mode   DeliveryMode
	filter func(Event) bool
	bus    *Bus

	mu     sync.Mutex
	queue  []Event
	notify chan struct{}
	done   chan struct{}
	closed bool
}

// deliver hands one event to this subscription without ever blocking Publish.
func (s *Subscription) deliver(e Event) {
	if s.mode == Durable {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.queue = append(s.queue, e)
		s.mu.Unlock()
		select {
		case s.notify <- struct{}{}:
		default:
		}
		return
	}
	// Lossy: non-blocking send, drop on full. Guard against send-on-closed.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.out <- e:
	default:
	}
}

// pump moves queued events to the output channel for durable subscribers,
// preserving order and never dropping. It owns closing s.out.
func (s *Subscription) pump() {
	defer close(s.out)
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			select {
			case <-s.notify:
			case <-s.done:
				return
			}
			continue
		}
		e := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		select {
		case s.out <- e:
		case <-s.done:
			return
		}
	}
}

// Close stops delivery to this subscription and closes C.
func (s *Subscription) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	isLossy := s.mode == Lossy
	s.mu.Unlock()

	if isLossy {
		// No pump owns the channel; close it directly. deliver() checks
		// s.closed under the same lock, so it will not send after this.
		close(s.out)
	} else {
		close(s.done) // pump exits and closes s.out
	}
	if s.bus != nil {
		s.bus.remove(s)
	}
}
