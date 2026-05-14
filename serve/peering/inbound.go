package peering

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// InvokeArgs is the JSON shape carried in an INVOKE frame's args payload.
// v1 is just a message string; structured input + tool args land later.
type InvokeArgs struct {
	Message string `json:"message"`
}

// DispatchStats is the accounting summary returned by a local Dispatcher
// when it finishes streaming a response. Mirrors the §3 accounting object
// proposed for AIRE final frames (see docs/peering-aire-spec-issues.md #3).
type DispatchStats struct {
	TokensIn  int
	TokensOut int
	CostUSD   float64
}

// Dispatcher is the narrow surface peering needs from Vega's interpreter:
// invoke a local agent and stream the response back chunk by chunk. Returns
// final accounting + any terminal error. The real implementation is wired
// in serve/server.go; tests use a fake.
type Dispatcher interface {
	Dispatch(ctx context.Context, agent, message string, emit func([]byte) error) (DispatchStats, error)
}

// InboundOp is the subset of aire.Operation that the inbound flow uses.
// Defined as an interface so unit tests can supply a fake without needing
// a live QUIC pair. serve/peering/aire_op.go adapts a real aire.Operation
// to satisfy it.
type InboundOp interface {
	OpID() uint64
	SendChunk(payload []byte) error
	SendError(code int, message string) error
	Close() error
}

// Error codes carried in the JSON payload of an outbound ERROR frame.
// Stable string codes so peers can switch on them without parsing prose.
const (
	ErrCodeDenied        = 4001
	ErrCodeMalformedArgs = 4002
	ErrCodeEmptyMessage  = 4003
	ErrCodeAgentFailed   = 5001
)

// HandleInbound runs the full inbound flow for one Invoke. It never returns
// an error to the caller — failures are reported to the peer via ERROR
// frames and recorded in the audit log. The non-nil return signature is
// kept for future expansion (e.g. transport-level errors worth propagating).
//
// Steps (see docs/peering-design.md §2.4):
//  1. Authorize → deny path: ERROR frame + denied audit row, return.
//  2. Decode args → malformed path: ERROR frame + error audit row, return.
//  3. Write started audit row.
//  4. Dispatch via dispatcher, streaming chunks to the peer.
//  5. Finalize audit row (status, tokens, duration).
//  6. Close the operation.
func HandleInbound(
	ctx context.Context,
	peerNodeID, peerHandle string,
	agentID string,
	args []byte,
	op InboundOp,
	store Store,
	dispatcher Dispatcher,
) error {
	start := time.Now()
	now := start.UTC()
	canonical := CanonicalizeAgent(agentID)

	defer func() { _ = op.Close() }()

	// 1. Authorize.
	decision, err := Authorize(store, peerNodeID, agentID, now)
	if err != nil {
		// Storage-level failures are themselves auditable: log as error.
		_, _ = store.InsertAudit(AuditEntry{
			Timestamp:    now,
			Direction:    DirectionInbound,
			PeerNodeID:   peerNodeID,
			PeerHandle:   peerHandle,
			Agent:        canonical,
			OpID:         int64(op.OpID()),
			Status:       AuditStatusError,
			DenialReason: err.Error(),
			DurationMS:   int(time.Since(start).Milliseconds()),
		})
		_ = op.SendError(ErrCodeAgentFailed, "internal error during authorization")
		return nil
	}
	if !decision.OK {
		_, _ = store.InsertAudit(AuditEntry{
			Timestamp:    now,
			Direction:    DirectionInbound,
			PeerNodeID:   peerNodeID,
			PeerHandle:   peerHandle,
			Agent:        canonical,
			OpID:         int64(op.OpID()),
			Status:       AuditStatusDenied,
			DenialReason: decision.DenialReason,
			DurationMS:   int(time.Since(start).Milliseconds()),
		})
		_ = op.SendError(ErrCodeDenied, decision.DenialReason)
		return nil
	}

	// 2. Decode args.
	var parsed InvokeArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		recordError(store, peerNodeID, peerHandle, canonical, op.OpID(), now, start,
			fmt.Sprintf("malformed args: %v", err))
		_ = op.SendError(ErrCodeMalformedArgs, "malformed args (expected JSON {message: string})")
		return nil
	}
	if parsed.Message == "" {
		recordError(store, peerNodeID, peerHandle, canonical, op.OpID(), now, start, "empty message")
		_ = op.SendError(ErrCodeEmptyMessage, "message is required and may not be empty")
		return nil
	}

	// 3. Audit start row (so an unfinished op is visible in the dashboard).
	auditID, err := store.InsertAudit(AuditEntry{
		Timestamp:  now,
		Direction:  DirectionInbound,
		PeerNodeID: peerNodeID,
		PeerHandle: peerHandle,
		Agent:      canonical,
		OpID:       int64(op.OpID()),
		Status:     AuditStatusStarted,
	})
	if err != nil {
		_ = op.SendError(ErrCodeAgentFailed, "audit write failed")
		return nil
	}

	// 4. Dispatch + stream.
	stats, dispatchErr := dispatcher.Dispatch(ctx, canonical, parsed.Message, op.SendChunk)

	// 5. Finalize audit + report failures.
	duration := int(time.Since(start).Milliseconds())
	if dispatchErr != nil {
		_ = store.FinalizeAudit(auditID, AuditFinalize{
			Status:       AuditStatusError,
			TokensIn:     stats.TokensIn,
			TokensOut:    stats.TokensOut,
			CostUSD:      stats.CostUSD,
			DurationMS:   duration,
			DenialReason: dispatchErr.Error(),
		})
		_ = op.SendError(ErrCodeAgentFailed, dispatchErr.Error())
		return nil
	}
	_ = store.FinalizeAudit(auditID, AuditFinalize{
		Status:     AuditStatusOK,
		TokensIn:   stats.TokensIn,
		TokensOut:  stats.TokensOut,
		CostUSD:    stats.CostUSD,
		DurationMS: duration,
	})
	return nil
}

// recordError writes a single audit row with status=error for paths that
// fail before the started row has been written (e.g. malformed args).
func recordError(store Store, peerNodeID, peerHandle, agent string, opID uint64,
	ts time.Time, start time.Time, reason string) {
	_, _ = store.InsertAudit(AuditEntry{
		Timestamp:    ts,
		Direction:    DirectionInbound,
		PeerNodeID:   peerNodeID,
		PeerHandle:   peerHandle,
		Agent:        agent,
		OpID:         int64(opID),
		Status:       AuditStatusError,
		DenialReason: reason,
		DurationMS:   int(time.Since(start).Milliseconds()),
	})
}
