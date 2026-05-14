package peering

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	aire "github.com/aire-protocol/aire-go"
	"github.com/google/uuid"
)

// jsonUnmarshal is aliased to keep node.go's switch readable.
var jsonUnmarshal = json.Unmarshal

// timeNow is a seam for tests that want to inject a clock. Production
// callers use it as a synonym for time.Now().UTC().
var timeNow = func() time.Time { return time.Now().UTC() }

// NodeConfig is the construction-time configuration for a peering Node. The
// runtime address + TLS config are passed to Start, not NewNode, so the
// Node can be built (and its NodeID exposed) before any network is opened.
type NodeConfig struct {
	// Store backs persistence (peers, grants, audit log, settings). Required.
	Store Store
	// Dispatcher invokes local agents on behalf of incoming peer Invokes.
	// Required. In production this is a thin adapter around the Vega
	// Interpreter; in tests it's a fake.
	Dispatcher Dispatcher
}

// Node is the peering runtime: it owns the local NodeID, the AIRE listener
// (when Started), and the outbound connection pool. Created via NewNode;
// network setup happens in Start; Stop drains.
type Node struct {
	cfg    NodeConfig
	nodeID string

	mu       sync.Mutex
	listener *aire.Listener
	started  bool
	stopped  bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewNode constructs a Node and loads (or generates) its persistent NodeID.
// The Node's NodeID is available via Node.NodeID() immediately afterwards.
func NewNode(cfg NodeConfig) (*Node, error) {
	if cfg.Store == nil {
		return nil, errors.New("peering: NewNode: store is required")
	}
	if cfg.Dispatcher == nil {
		return nil, errors.New("peering: NewNode: dispatcher is required")
	}
	id, err := LoadOrGenerateNodeID(cfg.Store)
	if err != nil {
		return nil, fmt.Errorf("peering: NewNode: load node id: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Node{
		cfg:    cfg,
		nodeID: id,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// NodeID returns the locally-persistent identifier surfaced to peers during
// the HELLO exchange. Stable across restarts; rotated only by an operator
// explicitly clearing the settings row.
func (n *Node) NodeID() string {
	return n.nodeID
}

// Start opens an AIRE listener on addr (e.g., ":4433") with the given TLS
// config and begins accepting peer connections in the background.
func (n *Node) Start(addr string, tlsConf *tls.Config) error {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return errors.New("peering: Node.Start: already started")
	}
	if n.stopped {
		n.mu.Unlock()
		return errors.New("peering: Node.Start: already stopped")
	}
	l, err := aire.Listen(addr, tlsConf)
	if err != nil {
		n.mu.Unlock()
		return err
	}
	n.listener = l
	n.started = true
	n.mu.Unlock()

	n.wg.Add(1)
	go n.acceptLoop()
	return nil
}

// Addr returns the listener's network address, or "" if Start hasn't run.
func (n *Node) Addr() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.listener == nil {
		return ""
	}
	return n.listener.Addr().String()
}

// localNodeConfig is the aire.NodeConfig advertised during every HELLO. The
// required vega.shared-secret/1 capability is the interop signal that both
// sides will exchange an HMAC auth-op before dispatch is accepted.
func (n *Node) localNodeConfig() aire.NodeConfig {
	return aire.NodeConfig{
		NodeID: n.nodeID,
		Capabilities: []aire.Capability{
			{Name: SharedSecretCapName, Version: 1, Required: true},
		},
	}
}

// SharedSecretCapName is the AIRE capability name peers advertise to commit
// to the shared-secret auth protocol implemented in auth.go. v0.1 stopgap;
// dropped when AIRE v0.2 DIDs ship.
const SharedSecretCapName = "vega.shared-secret/1"

// AgentIDVega is the single AIRE-level agent name a peer addresses for any
// dispatch. The target local agent name is carried inside InvokeArgs.Agent.
// Keeps the AIRE registry static; per-grant dynamics live in our ACL layer.
const AgentIDVega = "vega"

// AgentIDAuth is the well-known AIRE-level agent name a peer addresses for
// the HMAC challenge-response on a fresh connection.
const AgentIDAuth = "_aire/auth"

// acceptLoop pulls connections off the listener until Stop cancels the ctx.
// Each connection is served by a dedicated goroutine.
func (n *Node) acceptLoop() {
	defer n.wg.Done()
	for {
		conn, err := n.listener.Accept(n.ctx)
		if err != nil {
			return
		}
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			n.serveConn(conn)
		}()
	}
}

// connState tracks per-connection state that handlers (notably the auth-op
// handler) need to share with downstream dispatch.
type connState struct {
	peerNodeID string

	mu     sync.Mutex
	authed bool
}

// markAuthed flips authed=true. Called by the server-side auth-op after a
// successful HMAC verification.
func (s *connState) markAuthed() {
	s.mu.Lock()
	s.authed = true
	s.mu.Unlock()
}

// isAuthed reports whether the conn has completed the auth-op successfully.
func (s *connState) isAuthed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authed
}

// serveConn runs the handshake for one inbound connection, then loops on
// AcceptOperation, dispatching to either the auth-op handler or the main
// inbound handler.
func (n *Node) serveConn(conn *aire.Conn) {
	defer func() { _ = conn.Close() }()

	state, err := conn.Handshake(n.ctx, n.localNodeConfig())
	if err != nil {
		return
	}
	cs := &connState{peerNodeID: state.PeerNodeID}

	// Surface this peer's last-seen timestamp on every successful conn.
	// Failure to update is non-fatal — the conn proceeds either way.
	_ = n.cfg.Store.TouchPeerLastSeen(cs.peerNodeID, timeNow())

	for {
		op, err := conn.AcceptOperation(n.ctx)
		if err != nil {
			return
		}
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			n.serveOp(cs, op)
		}()
	}
}

// serveOp recognizes one inbound Operation: receive the INVOKE frame,
// decode its payload, and route by AgentID to either the auth-op or
// the dispatch flow.
func (n *Node) serveOp(cs *connState, op *aire.Operation) {
	io := NewAireInboundOp(op)
	f, err := op.Recv()
	if err != nil {
		_ = op.Close()
		return
	}
	if f.Type != aire.FrameInvoke {
		_ = op.Close()
		return
	}
	agentID, _, args, err := decodeInvokePayload(f.Payload)
	if err != nil {
		_ = io.SendError(ErrCodeMalformedArgs, "malformed INVOKE payload")
		_ = op.Close()
		return
	}

	switch agentID {
	case AgentIDAuth:
		n.serveAuthOp(cs, io, args)
	case AgentIDVega:
		if !cs.isAuthed() {
			_ = io.SendError(ErrCodeDenied, "auth required: call _aire/auth first")
			_ = op.Close()
			return
		}
		var ia InvokeArgs
		if err := jsonUnmarshal(args, &ia); err != nil {
			_ = io.SendError(ErrCodeMalformedArgs, "args must be JSON {agent, message}")
			_ = op.Close()
			return
		}
		// Look up peer for the handle (audit attribution).
		peer, _ := n.cfg.Store.GetPeer(cs.peerNodeID)
		handle := ""
		if peer != nil {
			handle = peer.Handle
		}
		_ = HandleInbound(n.ctx, cs.peerNodeID, handle, ia.Agent, ia.Message, io, n.cfg.Store, n.cfg.Dispatcher)
	default:
		_ = io.SendError(ErrCodeDenied, "unknown agent id: "+agentID)
		_ = op.Close()
	}
}

// Stop closes the listener (if Started) and waits for any in-flight
// handlers to drain. Safe to call multiple times and safe before Start.
func (n *Node) Stop() error {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return nil
	}
	n.stopped = true
	listener := n.listener
	n.listener = nil
	cancel := n.cancel
	n.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}
	n.wg.Wait()
	return nil
}

// LoadOrGenerateNodeID returns the locally-persistent NodeID, generating
// one on first call. The format is "vega:" + uuidv4 so peers can recognize
// it on sight as a Vega orchestrator (v0.2 DIDs will replace this).
func LoadOrGenerateNodeID(s Store) (string, error) {
	id, err := s.GetSetting(SettingNodeID)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	id = "vega:" + uuid.NewString()
	if err := s.SetSetting(SettingNodeID, id); err != nil {
		return "", err
	}
	return id, nil
}

// --- INVOKE payload codec ---
//
// aire-go does not export decodeInvokePayload (it's internal to Node.handleOperation).
// We re-implement the spec §3.3 INVOKE encoding here so the peering accept
// loop can decode payloads itself without going through aire.Node. The
// encoding is:
//   <agentID: varint(len)+bytes><opName: varint(len)+bytes><args: remaining bytes>

// encodeInvokePayload mirrors aire's internal encoder; exported for tests
// and used by the (forthcoming) outbound dial helper.
func encodeInvokePayload(agentID, opName string, args []byte) []byte {
	var buf []byte
	buf = aire.AppendVarint(buf, uint64(len(agentID)))
	buf = append(buf, agentID...)
	buf = aire.AppendVarint(buf, uint64(len(opName)))
	buf = append(buf, opName...)
	buf = append(buf, args...)
	return buf
}

// decodeInvokePayload parses the spec §3.3 INVOKE encoding produced by
// encodeInvokePayload (or by aire's internal encoder, which is bit-for-bit
// identical).
func decodeInvokePayload(payload []byte) (string, string, []byte, error) {
	agentID, n1, err := readVarintString(payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("INVOKE: agent-id: %w", err)
	}
	opName, n2, err := readVarintString(payload[n1:])
	if err != nil {
		return "", "", nil, fmt.Errorf("INVOKE: operation: %w", err)
	}
	args := append([]byte(nil), payload[n1+n2:]...)
	return agentID, opName, args, nil
}

// readVarintString decodes a varint-length-prefixed string from data,
// returning the string and the total bytes consumed (length-prefix +
// string bytes).
func readVarintString(data []byte) (string, int, error) {
	length, n, err := aire.ReadVarint(data)
	if err != nil {
		return "", 0, err
	}
	end := n + int(length)
	if end > len(data) {
		return "", 0, fmt.Errorf("truncated string: need %d bytes, have %d", length, len(data)-n)
	}
	return string(data[n:end]), end, nil
}
