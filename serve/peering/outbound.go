package peering

import (
	"encoding/json"
	"fmt"
	"io"
)

// FrameType is a peering-internal tag for the inbound frames the outbound
// reader recognizes. Defined here (rather than reusing aire.FrameType
// directly) so the outbound code path is wire-agnostic and unit-testable
// without standing up a QUIC pair. The production adapter in aire_op.go
// translates aire.FrameType → peering.FrameType.
type FrameType int

const (
	// FrameTypeStream carries a response chunk (raw bytes).
	FrameTypeStream FrameType = 1
	// FrameTypeError signals the remote side failed and is closing the op;
	// Payload is the JSON {code, message} written by inbound.go.
	FrameTypeError FrameType = 2
)

// Frame is the minimal envelope peering's outbound reader needs.
type Frame struct {
	Type    FrameType
	Payload []byte
}

// OutboundOp is the client side of an in-flight Invoke: Recv reads the next
// response frame from the peer, Close ends the op. tests use a fake; real
// callers wrap aire.Operation.
type OutboundOp interface {
	Recv() (Frame, error)
	Close() error
}

// RemoteError is the decoded contents of an ERROR frame from the peer.
type RemoteError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error renders the RemoteError for use as a Go error message.
func (e *RemoteError) Error() string {
	return fmt.Sprintf("peering: remote error (code=%d): %s", e.Code, e.Message)
}

// ReadStreamUntilDone reads frames from op until EOF or until the peer
// emits an ERROR frame. Each STREAM frame's payload is passed to onChunk.
// Unknown frame types are silently dropped (forward-compat for v0.3 CANCEL
// / BUDGET frames that haven't shipped yet).
//
// Return values:
//   - remErr != nil  → the peer told us it failed; transport was clean.
//   - err    != nil  → transport or onChunk error; op state is undefined.
//   - both nil       → success; remote completed normally.
//
// onChunk may be nil; chunks are then discarded silently. The op is closed
// on return regardless of outcome.
func ReadStreamUntilDone(op OutboundOp, onChunk func([]byte) error) (*RemoteError, error) {
	defer func() { _ = op.Close() }()
	for {
		f, err := op.Recv()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("peering: outbound recv: %w", err)
		}
		switch f.Type {
		case FrameTypeStream:
			if onChunk != nil {
				if err := onChunk(f.Payload); err != nil {
					return nil, fmt.Errorf("peering: onChunk: %w", err)
				}
			}
		case FrameTypeError:
			var re RemoteError
			if err := json.Unmarshal(f.Payload, &re); err != nil {
				return nil, fmt.Errorf("peering: malformed ERROR payload: %w", err)
			}
			return &re, nil
		default:
			// Forward-compat: ignore frame types we don't know.
		}
	}
}

// EncodeInvokeArgs is the canonical encoder for the args payload of an
// outbound Invoke (mirror of InvokeArgs decoding in inbound.go).
func EncodeInvokeArgs(message string) ([]byte, error) {
	return json.Marshal(InvokeArgs{Message: message})
}

// decodeInvokeArgs is the test helper to round-trip-check EncodeInvokeArgs.
func decodeInvokeArgs(data []byte, out *InvokeArgs) error {
	return json.Unmarshal(data, out)
}

// encodeError builds the JSON payload of an ERROR frame.
func encodeError(code int, message string) ([]byte, error) {
	return json.Marshal(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
}
