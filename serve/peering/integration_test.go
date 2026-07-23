package peering

import (
	"context"
	"strings"
	"testing"
	"time"

	aire "github.com/aire-protocol/aire-go"
)

// TestIntegration_TwoNodesEndToEnd stands up two real peering Nodes against
// each other on localhost, exchanges a single Invoke from A to B, and
// asserts both audit logs reflect the round trip. This is the smoke test
// that proves the full stack — Handshake, auth-op, dispatch, ACL, audit —
// composes correctly under real QUIC.
//
// Tests that don't need a live network are in node_test.go; this one is
// the only one that opens a UDP socket.
func TestIntegration_TwoNodesEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped under -short")
	}

	const (
		secret  = "shared-secret-for-the-integration-test-32!"
		message = "ping from node A"
		reply   = "pong from node B"
	)

	// Two stores, two NodeIDs, configured as peers of each other.
	storeA := newTestStore(t)
	storeB := newTestStore(t)
	dispB := &fakeDispatcher{chunks: []string{reply}, stats: DispatchStats{TokensIn: 5, TokensOut: 10}}
	dispA := &fakeDispatcher{} // A doesn't get invoked in this test

	nodeA, err := NewNode(NodeConfig{Store: storeA, Dispatcher: dispA})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := NewNode(NodeConfig{Store: storeB, Dispatcher: dispB})
	if err != nil {
		t.Fatal(err)
	}

	tlsA := aire.DevTLSConfig()
	tlsB := aire.DevTLSConfig()

	if err := nodeB.Start("127.0.0.1:0", tlsB); err != nil {
		t.Fatalf("nodeB.Start: %v", err)
	}
	t.Cleanup(func() { _ = nodeB.Stop() })
	if err := nodeA.Start("127.0.0.1:0", tlsA); err != nil {
		t.Fatalf("nodeA.Start: %v", err)
	}
	t.Cleanup(func() { _ = nodeA.Stop() })

	// Make each other peers + grant the agent we're invoking.
	peerB := Peer{
		NodeID:       nodeB.NodeID(),
		Handle:       "@b@localhost",
		Endpoint:     nodeB.Addr(),
		SharedSecret: secret,
		TrustLevel:   TrustScoped,
	}
	if err := storeA.UpsertPeer(peerB); err != nil {
		t.Fatal(err)
	}
	peerA := Peer{
		NodeID:       nodeA.NodeID(),
		Handle:       "@a@localhost",
		Endpoint:     nodeA.Addr(),
		SharedSecret: secret,
		TrustLevel:   TrustScoped,
	}
	if err := storeB.UpsertPeer(peerA); err != nil {
		t.Fatal(err)
	}
	if err := storeB.UpsertGrant(Grant{
		PeerNodeID: nodeA.NodeID(), LocalAgent: "researcher",
		MaxTokensPerOp: 8000, MaxOpsPerHour: 30, Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Build the client-side TLS config — aire.DevTLSConfig produces a
	// usable client config too (skips verify, sets ALPN).
	dialerTLS := aire.DevTLSConfig()
	dialer := NewDialer(storeA, nodeA.Signer(), dialerTLS)
	t.Cleanup(dialer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got strings.Builder
	remErr, err := dialer.SendToRemoteAgent(ctx, peerB, "researcher", message, func(b []byte) error {
		got.Write(b)
		return nil
	})
	if err != nil {
		t.Fatalf("SendToRemoteAgent: %v", err)
	}
	if remErr != nil {
		t.Fatalf("remote rejected: %s", remErr.Error())
	}
	if got.String() != reply {
		t.Fatalf("unexpected reply: got %q, want %q", got.String(), reply)
	}

	// Node B should have run the dispatcher with the canonical agent name + message.
	if dispB.gotAgent != "researcher" || dispB.gotMessage != message {
		t.Fatalf("nodeB dispatcher saw agent=%q message=%q", dispB.gotAgent, dispB.gotMessage)
	}

	// Both sides should have audit rows.
	rowsA, _ := storeA.ListAudit(AuditFilter{Limit: 10})
	if len(rowsA) != 1 || rowsA[0].Direction != DirectionOutbound || rowsA[0].Status != AuditStatusOK {
		t.Fatalf("nodeA outbound audit: %+v", rowsA)
	}
	rowsB, _ := storeB.ListAudit(AuditFilter{Limit: 10})
	if len(rowsB) != 1 || rowsB[0].Direction != DirectionInbound || rowsB[0].Status != AuditStatusOK {
		t.Fatalf("nodeB inbound audit: %+v", rowsB)
	}
}

func TestIntegration_DenialPropagatesToCaller(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped under -short")
	}
	const secret = "another-shared-secret-for-this-test-32!"

	storeA := newTestStore(t)
	storeB := newTestStore(t)
	dispB := &fakeDispatcher{}

	nodeA, _ := NewNode(NodeConfig{Store: storeA, Dispatcher: &fakeDispatcher{}})
	nodeB, _ := NewNode(NodeConfig{Store: storeB, Dispatcher: dispB})

	if err := nodeB.Start("127.0.0.1:0", aire.DevTLSConfig()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeB.Stop() })

	// Peers but NO grant on B → invokes should be denied.
	peerB := Peer{
		NodeID: nodeB.NodeID(), Handle: "@b", Endpoint: nodeB.Addr(),
		SharedSecret: secret, TrustLevel: TrustScoped,
	}
	_ = storeA.UpsertPeer(peerB)
	_ = storeB.UpsertPeer(Peer{
		NodeID: nodeA.NodeID(), Handle: "@a",
		SharedSecret: secret, TrustLevel: TrustScoped,
	})

	dialer := NewDialer(storeA, nodeA.Signer(), aire.DevTLSConfig())
	t.Cleanup(dialer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	remErr, err := dialer.SendToRemoteAgent(ctx, peerB, "researcher", "hi", nil)
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if remErr == nil {
		t.Fatal("expected a remote denial; got nil")
	}
	if remErr.Code != ErrCodeDenied {
		t.Fatalf("expected ErrCodeDenied, got %d (%s)", remErr.Code, remErr.Message)
	}
	if dispB.gotAgent != "" {
		t.Fatal("nodeB dispatcher must not be called when grant is missing")
	}
}
