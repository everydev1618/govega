package peering

import (
	"encoding/json"

	aire "github.com/aire-protocol/aire-go"
)

// aireInboundOp adapts a real *aire.Operation to the InboundOp interface
// peering's inbound flow needs. STREAM frames carry response chunks; ERROR
// frames carry a JSON {code, message} payload.
type aireInboundOp struct {
	op *aire.Operation
}

// NewAireInboundOp wraps an aire.Operation so it can be fed to HandleInbound.
func NewAireInboundOp(op *aire.Operation) InboundOp {
	return &aireInboundOp{op: op}
}

func (a *aireInboundOp) OpID() uint64 {
	return a.op.OpID
}

func (a *aireInboundOp) SendChunk(payload []byte) error {
	return a.op.Send(aire.Frame{Type: aire.FrameStream, Payload: payload})
}

func (a *aireInboundOp) SendError(code int, message string) error {
	payload, err := json.Marshal(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
	if err != nil {
		return err
	}
	return a.op.Send(aire.Frame{Type: aire.FrameError, Payload: payload})
}

func (a *aireInboundOp) Close() error {
	return a.op.Close()
}
