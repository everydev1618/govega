package peering

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aire "github.com/aire-protocol/aire-go"
)

// authChallenge is what the server emits as the first STREAM frame of the
// auth op. Nonce is fresh; the client must HMAC over (peer_node_id || nonce
// || timestamp) using its shared secret and reply.
type authChallenge struct {
	Nonce []byte `json:"nonce"`
}

// authResponse is what the client emits as the second STREAM frame of the
// auth op, after computing the HMAC under its shared secret.
type authResponse struct {
	NodeID    string `json:"node_id"`
	Nonce     []byte `json:"nonce"`
	Timestamp int64  `json:"ts_nanos"`
	HMAC      []byte `json:"hmac"`
}

// serveAuthOp is the server side of the challenge-response. Stream frames
// carry the JSON-encoded challenge and response; an "ok" or error frame
// terminates the op.
func (n *Node) serveAuthOp(cs *connState, op InboundOp, _ []byte) {
	defer func() { _ = op.Close() }()

	// Generate a fresh nonce; send as the challenge.
	nonce, err := NewNonce()
	if err != nil {
		_ = op.SendError(ErrCodeAgentFailed, "rng failed")
		return
	}
	chal, err := json.Marshal(authChallenge{Nonce: nonce})
	if err != nil {
		_ = op.SendError(ErrCodeAgentFailed, "encode challenge")
		return
	}
	if err := op.SendChunk(chal); err != nil {
		return
	}

	// Receive response. serveAuthOp is invoked from serveOp which already
	// consumed the INVOKE frame; the next frame on this op should be the
	// client's STREAM-encoded authResponse. We need raw aire.Op access for
	// Recv — accomplished by unwrapping via interface assertion.
	rawOp, ok := op.(*aireInboundOp)
	if !ok {
		_ = op.SendError(ErrCodeAgentFailed, "internal: op type")
		return
	}
	respFrame, err := rawOp.op.Recv()
	if err != nil {
		return
	}
	if respFrame.Type != aire.FrameStream {
		_ = op.SendError(ErrCodeMalformedArgs, "expected STREAM response")
		return
	}

	var resp authResponse
	if err := json.Unmarshal(respFrame.Payload, &resp); err != nil {
		_ = op.SendError(ErrCodeMalformedArgs, "malformed auth response: "+err.Error())
		return
	}
	// The peer's claimed NodeID must match the one bound at handshake.
	if resp.NodeID != cs.peerNodeID {
		_ = op.SendError(ErrCodeDenied, "auth NodeID does not match handshake NodeID")
		return
	}
	peer, err := n.cfg.Store.GetPeer(cs.peerNodeID)
	if err != nil || peer == nil {
		_ = op.SendError(ErrCodeDenied, "unknown peer")
		return
	}

	claim := AuthClaim{
		NodeID:    resp.NodeID,
		Nonce:     resp.Nonce,
		Timestamp: time.Unix(0, resp.Timestamp),
	}
	if err := VerifyAuth([]byte(peer.SharedSecret), cs.peerNodeID, claim, resp.HMAC, time.Now().UTC()); err != nil {
		_ = op.SendError(ErrCodeDenied, "auth failed: "+err.Error())
		return
	}

	// All good. Mark the conn authed and signal the peer.
	cs.markAuthed()
	_ = op.SendChunk([]byte(`{"ok":true}`))
}

// runClientAuth is the client side of the auth-op. Used by the outbound
// dial helper after a successful Handshake. The caller has already verified
// the peer's HELLO NodeID matches the expected peer; this function just
// proves the local side's possession of the shared secret.
func runClientAuth(ctx context.Context, conn *aire.Conn, secret []byte, localNodeID string) error {
	op, err := conn.Invoke(ctx, AgentIDAuth, "challenge", nil)
	if err != nil {
		return fmt.Errorf("open auth op: %w", err)
	}
	defer func() { _ = op.Close() }()

	// Read challenge.
	f, err := op.Recv()
	if err != nil {
		return fmt.Errorf("recv challenge: %w", err)
	}
	if f.Type == aire.FrameError {
		return fmt.Errorf("peer rejected auth: %s", string(f.Payload))
	}
	if f.Type != aire.FrameStream {
		return fmt.Errorf("unexpected challenge frame type: %v", f.Type)
	}
	var chal authChallenge
	if err := json.Unmarshal(f.Payload, &chal); err != nil {
		return fmt.Errorf("decode challenge: %w", err)
	}

	// Compute HMAC and send response.
	now := time.Now().UTC()
	claim := AuthClaim{
		NodeID:    localNodeID,
		Nonce:     chal.Nonce,
		Timestamp: now,
	}
	mac := ComputeAuth(secret, claim)
	resp := authResponse{
		NodeID:    localNodeID,
		Nonce:     chal.Nonce,
		Timestamp: now.UnixNano(),
		HMAC:      mac,
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := op.Send(aire.Frame{Type: aire.FrameStream, Payload: payload}); err != nil {
		return fmt.Errorf("send response: %w", err)
	}

	// Read OK / error.
	final, err := op.Recv()
	if err != nil {
		return fmt.Errorf("recv ok: %w", err)
	}
	if final.Type == aire.FrameError {
		return fmt.Errorf("peer rejected auth: %s", string(final.Payload))
	}
	return nil
}
