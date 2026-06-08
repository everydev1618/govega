// Package aire is govega's adapter for the AIRE peer-to-peer agent protocol.
//
// AIRE (https://github.com/aire-protocol/aire-spec) is a QUIC-native
// agent-to-agent protocol with per-stream identity, multiplexed Operations
// without head-of-line blocking, semantic cancellation, and budget-aware
// backpressure. This package mirrors the shape of mcp/ — a Client that opens
// outbound connections to remote AIRE-speaking peers — and adds Vega
// ergonomics: a connection pool keyed by resolved endpoint and pluggable
// reference resolution (handles, DIDs).
//
// Per the AIRE governance contract, the upstream library
// (github.com/aire-protocol/aire-go) MUST NOT depend on Vega. The dependency
// always points one way: Vega imports AIRE, never the reverse.
package aire

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"sync"

	aireproto "github.com/aire-protocol/aire-go"
)

// ResolveFunc maps a reference (handle like "agent@host", or DID like
// "did:web:...") to a fully resolved peer Address. Callers may inject a
// custom ResolveFunc for testing or to plug in a non-default resolver.
type ResolveFunc func(ctx context.Context, ref string) (*aireproto.Address, error)

// Client is a Vega-side AIRE client. One Client manages a pool of outbound
// AIRE connections (one per resolved endpoint) and dispatches Operations to
// remote agents identified by handle or DID.
//
// Client is safe for concurrent use.
type Client struct {
	// NodeID is the local node's identifier sent in HELLO. It SHOULD be a
	// DID (per aire-spec §5); callers may use a did:key for ephemeral
	// identities or a did:web for long-lived ones.
	NodeID string

	// Resolve maps a reference to a peer Address. If nil, the package-level
	// aireproto.Resolve (DNS TXT + HTTPS .well-known + DID Document fetch)
	// is used.
	Resolve ResolveFunc

	// TLSConfig is used for outbound QUIC connections. If nil, a development
	// self-signed config is used (NOT FOR PRODUCTION).
	TLSConfig *tls.Config

	mu    sync.Mutex
	conns map[string]*aireproto.Conn // key: resolved endpoint
}

// NewClient creates a Client with the given NodeID. The NodeID should be a
// DID; for development, a did:key is sufficient.
func NewClient(nodeID string) *Client {
	return &Client{
		NodeID: nodeID,
		conns:  make(map[string]*aireproto.Conn),
	}
}

// Send invokes a remote AIRE agent and returns the first reply frame's
// payload.
//
// ref identifies the peer: a handle ("agent@host", "@agent@host") or a DID
// ("did:web:...", "did:key:..."). agentID names the agent on that peer; if
// empty, the AgentID resolved from the reference is used. opName is the
// operation name (carried in the INVOKE payload). args is the operation's
// argument bytes.
//
// Send is suitable for request/reply patterns. For streamed responses, use
// the lower-level aireproto.Conn.Invoke directly (this method reads exactly
// one reply frame).
func (c *Client) Send(ctx context.Context, ref, agentID, opName string, args []byte) ([]byte, error) {
	addr, err := c.resolveRef(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("aire: resolve %q: %w", ref, err)
	}
	if agentID == "" {
		agentID = addr.AgentID
	}
	if agentID == "" {
		return nil, fmt.Errorf("aire: no agentID provided and resolved Address has none for %q", ref)
	}

	conn, err := c.getOrDialConn(ctx, addr.Endpoint)
	if err != nil {
		return nil, err
	}

	op, err := conn.Invoke(ctx, agentID, opName, args)
	if err != nil {
		return nil, fmt.Errorf("aire: invoke %s/%s on %s: %w", agentID, opName, addr.Endpoint, err)
	}
	defer func() { _ = op.Close() }()

	frame, err := op.Recv()
	if err != nil {
		return nil, fmt.Errorf("aire: recv %s/%s on %s: %w", agentID, opName, addr.Endpoint, err)
	}
	return frame.Payload, nil
}

// SendStream invokes a remote agent and returns a channel of inbound frame
// payloads plus an error channel. The payload channel closes when the
// remote operation closes (FIN); the error channel is non-empty only if
// the operation aborted abnormally.
//
// Callers must drain the payload channel even on error to release the
// underlying QUIC stream.
func (c *Client) SendStream(ctx context.Context, ref, agentID, opName string, args []byte) (<-chan []byte, <-chan error, error) {
	addr, err := c.resolveRef(ctx, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("aire: resolve %q: %w", ref, err)
	}
	if agentID == "" {
		agentID = addr.AgentID
	}
	if agentID == "" {
		return nil, nil, fmt.Errorf("aire: no agentID provided and resolved Address has none for %q", ref)
	}

	conn, err := c.getOrDialConn(ctx, addr.Endpoint)
	if err != nil {
		return nil, nil, err
	}

	op, err := conn.Invoke(ctx, agentID, opName, args)
	if err != nil {
		return nil, nil, fmt.Errorf("aire: invoke %s/%s on %s: %w", agentID, opName, addr.Endpoint, err)
	}

	payloads := make(chan []byte, 16)
	errs := make(chan error, 1)

	go func() {
		defer func() { _ = op.Close() }()
		defer close(payloads)
		defer close(errs)
		for {
			f, err := op.Recv()
			if err != nil {
				if err == io.EOF {
					return
				}
				errs <- err
				return
			}
			select {
			case payloads <- f.Payload:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
	}()

	return payloads, errs, nil
}

// Close drains all peer connections held by the Client. Subsequent Sends
// will redial as needed.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var firstErr error
	for ep, conn := range c.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(c.conns, ep)
	}
	return firstErr
}

func (c *Client) resolveRef(ctx context.Context, ref string) (*aireproto.Address, error) {
	if c.Resolve != nil {
		return c.Resolve(ctx, ref)
	}
	return aireproto.Resolve(ctx, ref)
}

func (c *Client) getOrDialConn(ctx context.Context, endpoint string) (*aireproto.Conn, error) {
	c.mu.Lock()
	if conn, ok := c.conns[endpoint]; ok {
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()

	tlsConf := c.TLSConfig
	if tlsConf == nil {
		tlsConf = aireproto.DevTLSConfig()
	}

	conn, err := aireproto.Dial(ctx, endpoint, tlsConf)
	if err != nil {
		return nil, fmt.Errorf("aire: dial %s: %w", endpoint, err)
	}
	if _, err := conn.Handshake(ctx, aireproto.NodeConfig{NodeID: c.NodeID}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("aire: handshake %s: %w", endpoint, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.conns[endpoint]; ok {
		_ = conn.Close()
		return existing, nil
	}
	c.conns[endpoint] = conn
	return conn, nil
}
