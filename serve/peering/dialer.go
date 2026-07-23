package peering

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	aire "github.com/aire-protocol/aire-go"
)

// Dialer opens authenticated AIRE connections to peer orchestrators and
// dispatches outbound Invokes. Conns are cached per peer NodeID so repeated
// Invokes amortize the handshake + auth cost; a failed dial drops the cache
// entry so the next call reopens.
type Dialer struct {
	store       Store
	signer      aire.Signer
	localNodeID string
	tlsConf     *tls.Config

	mu    sync.Mutex
	conns map[string]*aire.Conn // peerNodeID → live conn
}

// NewDialer constructs a Dialer using the node's identity signer (its DID is
// the local NodeID in signed HELLOs — pass Node.Signer() so inbound and
// outbound present the same identity). tlsConf MUST include the AIRE ALPN
// (use aire.DevTLSConfig for dev; provision proper certs in prod).
func NewDialer(store Store, signer aire.Signer, tlsConf *tls.Config) *Dialer {
	return &Dialer{
		store:       store,
		signer:      signer,
		localNodeID: signer.DID(),
		tlsConf:     tlsConf,
		conns:       make(map[string]*aire.Conn),
	}
}

// SendToRemoteAgent dials the peer (or reuses a cached conn), opens a dispatch
// invoke addressed to AgentIDVega with InvokeArgs carrying the target local
// agent + message, and feeds response chunks into onChunk until completion.
//
// Returns the remote error (if the peer sent an ERROR frame) and any
// transport error. A successful call has remErr == nil && err == nil.
func (d *Dialer) SendToRemoteAgent(
	ctx context.Context,
	peer Peer,
	agent, message string,
	onChunk func([]byte) error,
) (*RemoteError, error) {
	return d.SendToRemoteAgentAs(ctx, peer, agent, message, "", onChunk)
}

// SendToRemoteAgentAs is SendToRemoteAgent carrying an opaque on-behalf-of
// credential (e.g. a LYRA Entrustment Credential): "this invoke acts for the
// human this credential names". Peering transports it verbatim; the remote
// side's dispatcher decides what it means.
func (d *Dialer) SendToRemoteAgentAs(
	ctx context.Context,
	peer Peer,
	agent, message, onBehalfOf string,
	onChunk func([]byte) error,
) (*RemoteError, error) {
	conn, err := d.getConn(ctx, peer)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	args, err := json.Marshal(InvokeArgs{Agent: agent, Message: message, OnBehalfOf: onBehalfOf})
	if err != nil {
		return nil, fmt.Errorf("encode args: %w", err)
	}
	op, err := conn.Invoke(ctx, AgentIDVega, "send", args)
	if err != nil {
		d.dropConn(peer.NodeID)
		return nil, fmt.Errorf("invoke: %w", err)
	}
	out := NewAireOutboundOp(op)

	// Write outbound audit row before reading (so unfinished outbound ops
	// are visible). Status is updated when ReadStreamUntilDone returns.
	auditID, _ := d.store.InsertAudit(AuditEntry{
		Timestamp:  time.Now().UTC(),
		Direction:  DirectionOutbound,
		PeerNodeID: peer.NodeID,
		PeerHandle: peer.Handle,
		Agent:      CanonicalizeAgent(agent),
		OpID:       int64(op.OpID),
		Status:     AuditStatusStarted,
	})
	start := time.Now()

	remErr, readErr := ReadStreamUntilDone(out, onChunk)
	finalize := AuditFinalize{
		DurationMS: int(time.Since(start).Milliseconds()),
	}
	switch {
	case readErr != nil:
		finalize.Status = AuditStatusError
		finalize.DenialReason = readErr.Error()
	case remErr != nil:
		// A peer-sourced error (denial, agent fail). Audit as such.
		switch remErr.Code {
		case ErrCodeDenied:
			finalize.Status = AuditStatusDenied
		default:
			finalize.Status = AuditStatusError
		}
		finalize.DenialReason = remErr.Message
	default:
		finalize.Status = AuditStatusOK
	}
	if auditID != 0 {
		_ = d.store.FinalizeAudit(auditID, finalize)
	}
	return remErr, readErr
}

// getConn returns a live, authed conn to peer — reusing the cached entry
// when available, dialing + running the HELLO + auth-op when not.
func (d *Dialer) getConn(ctx context.Context, peer Peer) (*aire.Conn, error) {
	d.mu.Lock()
	if c, ok := d.conns[peer.NodeID]; ok {
		d.mu.Unlock()
		return c, nil
	}
	d.mu.Unlock()

	conn, err := aire.Dial(ctx, endpointHostPort(peer.Endpoint), d.tlsConf)
	if err != nil {
		return nil, err
	}
	state, err := conn.Handshake(ctx, aire.NodeConfig{
		Signer: d.signer,
		Capabilities: []aire.Capability{
			{Name: SharedSecretCapName, Version: 1, Required: true},
		},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if state.PeerNodeID != peer.NodeID {
		_ = conn.Close()
		return nil, fmt.Errorf("peer NodeID mismatch: got %q, expected %q", state.PeerNodeID, peer.NodeID)
	}

	if err := runClientAuth(ctx, conn, []byte(peer.SharedSecret), d.localNodeID); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("auth: %w", err)
	}

	d.mu.Lock()
	d.conns[peer.NodeID] = conn
	d.mu.Unlock()
	return conn, nil
}

// dropConn removes a cached conn so the next call to getConn redials.
func (d *Dialer) dropConn(peerNodeID string) {
	d.mu.Lock()
	if c, ok := d.conns[peerNodeID]; ok {
		_ = c.Close()
		delete(d.conns, peerNodeID)
	}
	d.mu.Unlock()
}

// Close shuts down every cached conn. Safe to call multiple times.
func (d *Dialer) Close() {
	d.mu.Lock()
	for id, c := range d.conns {
		_ = c.Close()
		delete(d.conns, id)
	}
	d.mu.Unlock()
}

// endpointHostPort strips an optional "quic://" scheme so aire.Dial gets a
// raw host:port. Belt-and-braces: store users may save endpoints either way.
func endpointHostPort(endpoint string) string {
	const scheme = "quic://"
	if len(endpoint) > len(scheme) && endpoint[:len(scheme)] == scheme {
		return endpoint[len(scheme):]
	}
	return endpoint
}
