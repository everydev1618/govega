package aire_test

import (
	"context"
	"testing"
	"time"

	aireproto "github.com/aire-protocol/aire-go"
	vaire "github.com/everydev1618/govega/aire"
)

// TestClient_Send_RoundTrip stands up a remote AIRE node hosting an "echo"
// agent and verifies the Vega-side Client can dial it, handshake, invoke the
// agent, and receive the reply — modeling the topology of two govega
// instances collaborating over AIRE.
func TestClient_Send_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	remote := aireproto.NewNode(aireproto.NodeConfig{})
	defer func() { _ = remote.Stop() }()
	if err := remote.RegisterAgent("echo", aireproto.AgentFunc(func(_ context.Context, inv *aireproto.Invoke) error {
		return inv.Op.Send(aireproto.Frame{
			Type:    aireproto.FrameStream,
			Payload: append([]byte("echo:"), inv.Args...),
		})
	})); err != nil {
		t.Fatalf("remote RegisterAgent: %v", err)
	}
	if err := remote.Listen("127.0.0.1:0", aireproto.DevTLSConfig()); err != nil {
		t.Fatalf("remote Listen: %v", err)
	}

	client := vaire.NewClient()
	client.TLSConfig = aireproto.DevTLSConfig()
	client.Resolve = func(_ context.Context, ref string) (*aireproto.Address, error) {
		return &aireproto.Address{
			DID:      "did:web:example.com:agents:echo",
			Endpoint: remote.Addr(),
			AgentID:  "echo",
		}, nil
	}
	defer func() { _ = client.Close() }()

	payload, err := client.Send(ctx, "@echo@example.com", "" /* use resolved AgentID */, "ping", []byte("hello"))
	if err != nil {
		t.Fatalf("client.Send: %v", err)
	}
	if string(payload) != "echo:hello" {
		t.Errorf("payload = %q, want echo:hello", payload)
	}
}

// TestClient_PoolsConnections proves that two Sends to the same resolved
// endpoint reuse one underlying QUIC connection (one handshake), rather than
// dialing fresh each call.
func TestClient_PoolsConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	remote := aireproto.NewNode(aireproto.NodeConfig{})
	defer func() { _ = remote.Stop() }()
	if err := remote.RegisterAgent("counter", aireproto.AgentFunc(func(_ context.Context, inv *aireproto.Invoke) error {
		return inv.Op.Send(aireproto.Frame{Type: aireproto.FrameStream, Payload: []byte("ok")})
	})); err != nil {
		t.Fatalf("remote RegisterAgent: %v", err)
	}
	if err := remote.Listen("127.0.0.1:0", aireproto.DevTLSConfig()); err != nil {
		t.Fatalf("remote Listen: %v", err)
	}

	// Indirect proof of pooling: 50 Sends should run faster than the time
	// to handshake 50 fresh QUIC connections. If the pool is broken, total
	// time scales linearly with handshake cost rather than with frame RTT.
	client := vaire.NewClient()
	client.TLSConfig = aireproto.DevTLSConfig()
	client.Resolve = func(_ context.Context, _ string) (*aireproto.Address, error) {
		return &aireproto.Address{
			Endpoint: remote.Addr(),
			AgentID:  "counter",
		}, nil
	}
	defer func() { _ = client.Close() }()

	const n = 50
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := client.Send(ctx, "x", "", "go", nil); err != nil {
			t.Fatalf("Send #%d: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	// Generous bound: even slow CI shouldn't take >2s for 50 multiplexed
	// invocations on one conn. Re-dialing each time would be much slower.
	if elapsed > 2*time.Second {
		t.Errorf("50 Sends took %v; suggests connection pool is not reusing the conn", elapsed)
	}
}
