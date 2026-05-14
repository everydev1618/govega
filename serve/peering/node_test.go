package peering

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoadOrGenerateNodeID_GeneratesOnFirstCall(t *testing.T) {
	s := newTestStore(t)
	id, err := LoadOrGenerateNodeID(s)
	if err != nil {
		t.Fatalf("LoadOrGenerateNodeID: %v", err)
	}
	if !strings.HasPrefix(id, "vega:") {
		t.Fatalf("NodeID should be prefixed 'vega:', got %q", id)
	}
	if len(id) < len("vega:")+8 {
		t.Fatalf("NodeID suspiciously short: %q", id)
	}
}

func TestLoadOrGenerateNodeID_StableAcrossCalls(t *testing.T) {
	s := newTestStore(t)
	a, _ := LoadOrGenerateNodeID(s)
	b, _ := LoadOrGenerateNodeID(s)
	if a != b {
		t.Fatalf("NodeID drifted: %q != %q", a, b)
	}
}

func TestLoadOrGenerateNodeID_RespectsExistingValue(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetSetting(SettingNodeID, "vega:preexisting-id"); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrGenerateNodeID(s)
	if err != nil {
		t.Fatal(err)
	}
	if id != "vega:preexisting-id" {
		t.Fatalf("did not respect existing value: %q", id)
	}
}

func TestDecodeInvokePayload_RoundTrip(t *testing.T) {
	cases := []struct {
		agentID, opName string
		args            []byte
	}{
		{"vega", "send", []byte(`{"agent":"researcher","message":"hi"}`)},
		{"", "", []byte{}},
		{"a/b/c", "doit", []byte{0xff, 0x00, 0x01}},
	}
	for _, c := range cases {
		encoded := encodeInvokePayload(c.agentID, c.opName, c.args)
		gotAgent, gotOp, gotArgs, err := decodeInvokePayload(encoded)
		if err != nil {
			t.Errorf("decode(%q, %q): %v", c.agentID, c.opName, err)
			continue
		}
		if gotAgent != c.agentID || gotOp != c.opName {
			t.Errorf("got agent=%q op=%q, want %q/%q", gotAgent, gotOp, c.agentID, c.opName)
		}
		if !bytes.Equal(gotArgs, c.args) {
			t.Errorf("args mismatch: got %v, want %v", gotArgs, c.args)
		}
	}
}

func TestDecodeInvokePayload_MalformedRejected(t *testing.T) {
	// A varint declaring 100 bytes of agent ID followed by 1 byte is short.
	short := []byte{0x40, 0x64, 'x'} // 2-byte varint = 100; only one byte of data
	if _, _, _, err := decodeInvokePayload(short); err == nil {
		t.Fatal("expected error on truncated payload")
	}
}

func TestNodeConfig_RequiresStore(t *testing.T) {
	cfg := NodeConfig{}
	_, err := NewNode(cfg)
	if err == nil {
		t.Fatal("NewNode should require a non-nil Store")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Fatalf("error should mention store, got %q", err.Error())
	}
}

func TestNodeConfig_RequiresDispatcher(t *testing.T) {
	s := newTestStore(t)
	cfg := NodeConfig{Store: s}
	_, err := NewNode(cfg)
	if err == nil {
		t.Fatal("NewNode should require a non-nil Dispatcher")
	}
	if !strings.Contains(err.Error(), "dispatcher") {
		t.Fatalf("error should mention dispatcher, got %q", err.Error())
	}
}

func TestNode_NodeID_LoadedAtConstruction(t *testing.T) {
	s := newTestStore(t)
	disp := &fakeDispatcher{}
	n, err := NewNode(NodeConfig{Store: s, Dispatcher: disp})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(n.NodeID(), "vega:") {
		t.Fatalf("node should expose its NodeID; got %q", n.NodeID())
	}
	// And persisted in store.
	stored, _ := s.GetSetting(SettingNodeID)
	if stored != n.NodeID() {
		t.Fatalf("NodeID not persisted: stored=%q, node=%q", stored, n.NodeID())
	}
}

func TestNode_StopBeforeStartIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	n, _ := NewNode(NodeConfig{Store: s, Dispatcher: &fakeDispatcher{}})
	if err := n.Stop(); err != nil {
		t.Fatalf("Stop before Start should be ok, got %v", err)
	}
	if err := n.Stop(); err != nil {
		t.Fatalf("second Stop should be ok, got %v", err)
	}
}
