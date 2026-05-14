package peering

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"

	aire "github.com/aire-protocol/aire-go"
	"github.com/google/uuid"
)

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
// config and begins accepting peer connections in the background. Idempotent
// in the sense that calling Start twice on the same Node returns an error
// rather than starting two listeners.
//
// The accept loop + inbound dispatch + auth-op handling are wired in a
// follow-on commit. This skeleton establishes the lifecycle contract.
func (n *Node) Start(addr string, tlsConf *tls.Config) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.started {
		return errors.New("peering: Node.Start: already started")
	}
	if n.stopped {
		return errors.New("peering: Node.Start: already stopped")
	}
	l, err := aire.Listen(addr, tlsConf)
	if err != nil {
		return err
	}
	n.listener = l
	n.started = true
	// Accept loop intentionally not started yet — wired in next commit
	// once inbound dispatch + auth-op handler are merged.
	return nil
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
