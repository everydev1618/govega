package serve

import (
	"testing"
	"time"
)

// recvBroker reads one event from a broker subscription with a timeout so a
// missing publish fails fast instead of hanging the suite.
func recvBroker(t *testing.T, sub chan BrokerEvent) (BrokerEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-sub:
		return ev, ok
	case <-time.After(2 * time.Second):
		return BrokerEvent{}, false
	}
}

// TestChannelPostMirrorsToBroker pins the idle live-viewing fix: when an agent
// posts to a channel via post_to_channel, the post must reach the global event
// broker so a client with no active per-channel stream (someone just watching
// the channel) sees it live. Before this, the post only hit the per-channel
// stream, which idle viewers don't subscribe to — so agent posts appeared only
// on reload.
func TestChannelPostMirrorsToBroker(t *testing.T) {
	s := channelsTestServer(t)
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	postCb, _, _ := s.buildChannelCallbacks()
	postCb("musolist-launch", "quill", "sourcing sweep done", 7, nil)

	ev, ok := recvBroker(t, sub)
	if !ok {
		t.Fatal("no broker event published for a top-level channel post")
	}
	if ev.Type != "channel.message" {
		t.Fatalf("broker event type = %q, want channel.message", ev.Type)
	}
	ce, ok := ev.Data.(ChannelEvent)
	if !ok {
		t.Fatalf("broker event Data = %T, want ChannelEvent", ev.Data)
	}
	if ce.Channel != "musolist-launch" || ce.Agent != "quill" || ce.Content != "sourcing sweep done" || ce.MessageID != 7 {
		t.Fatalf("broker payload mismatch: %+v", ce)
	}
}

// TestChannelThreadReplyNotMirrored pins that thread replies are NOT flooded to
// the global broker — only top-level posts are mirrored, since the broker fans
// out to every connected client and thread replies belong to the thread panel.
func TestChannelThreadReplyNotMirrored(t *testing.T) {
	s := channelsTestServer(t)
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	postCb, _, _ := s.buildChannelCallbacks()
	tid := int64(3)
	postCb("musolist-launch", "quill", "in-thread reply", 8, &tid)

	if ev, ok := recvBroker(t, sub); ok {
		t.Fatalf("thread reply should not hit the broker, got %q", ev.Type)
	}
}
