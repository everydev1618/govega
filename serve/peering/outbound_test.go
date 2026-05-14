package peering

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeOutboundOp serves a scripted sequence of frames to Recv(), then EOF.
type fakeOutboundOp struct {
	frames  []Frame
	pos     int
	recvErr error
	closed  bool
}

func (f *fakeOutboundOp) Recv() (Frame, error) {
	if f.recvErr != nil {
		return Frame{}, f.recvErr
	}
	if f.pos >= len(f.frames) {
		return Frame{}, io.EOF
	}
	fr := f.frames[f.pos]
	f.pos++
	return fr, nil
}

func (f *fakeOutboundOp) Close() error {
	f.closed = true
	return nil
}

func chunk(b string) Frame {
	return Frame{Type: FrameTypeStream, Payload: []byte(b)}
}

func errFrame(code int, msg string) Frame {
	return Frame{Type: FrameTypeError, Payload: mustEncodeError(code, msg)}
}

// --- tests ---

func TestReadStreamUntilDone_HappyPath(t *testing.T) {
	op := &fakeOutboundOp{frames: []Frame{chunk("hello, "), chunk("world")}}
	var got strings.Builder
	remErr, err := ReadStreamUntilDone(op, func(b []byte) error {
		got.Write(b)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadStreamUntilDone: %v", err)
	}
	if remErr != nil {
		t.Fatalf("expected no remote error, got %+v", remErr)
	}
	if got.String() != "hello, world" {
		t.Fatalf("accumulated = %q", got.String())
	}
	if !op.closed {
		t.Fatal("op should be closed after read")
	}
}

func TestReadStreamUntilDone_RemoteErrorFrame(t *testing.T) {
	op := &fakeOutboundOp{frames: []Frame{
		chunk("partial"),
		errFrame(ErrCodeDenied, "rate limit exceeded"),
	}}
	var got strings.Builder
	remErr, err := ReadStreamUntilDone(op, func(b []byte) error {
		got.Write(b)
		return nil
	})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}
	if remErr == nil {
		t.Fatal("expected remote error to be returned")
	}
	if remErr.Code != ErrCodeDenied || !strings.Contains(remErr.Message, "rate") {
		t.Fatalf("remote error not decoded: %+v", remErr)
	}
	// Pre-error chunks should still have been delivered.
	if got.String() != "partial" {
		t.Fatalf("pre-error chunk lost: %q", got.String())
	}
}

func TestReadStreamUntilDone_TransportError(t *testing.T) {
	op := &fakeOutboundOp{recvErr: errors.New("conn reset")}
	remErr, err := ReadStreamUntilDone(op, nil)
	if err == nil || !strings.Contains(err.Error(), "conn reset") {
		t.Fatalf("expected transport error, got err=%v remErr=%+v", err, remErr)
	}
}

func TestReadStreamUntilDone_OnChunkErrorAborts(t *testing.T) {
	op := &fakeOutboundOp{frames: []Frame{chunk("a"), chunk("b")}}
	_, err := ReadStreamUntilDone(op, func(b []byte) error {
		return errors.New("downstream cancelled")
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected callback error to propagate, got %v", err)
	}
}

func TestReadStreamUntilDone_IgnoresUnknownFrameTypes(t *testing.T) {
	// Forward-compat: future frame types (CANCEL, BUDGET) MUST NOT make
	// today's clients fail. They're ignored until support is added.
	op := &fakeOutboundOp{frames: []Frame{
		{Type: FrameType(99), Payload: []byte("future")},
		chunk("ok"),
	}}
	var got strings.Builder
	_, err := ReadStreamUntilDone(op, func(b []byte) error {
		got.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "ok" {
		t.Fatalf("unknown frame not ignored: %q", got.String())
	}
}

func TestEncodeInvokeArgs_RoundTrip(t *testing.T) {
	encoded, err := EncodeInvokeArgs("hello, peer")
	if err != nil {
		t.Fatalf("EncodeInvokeArgs: %v", err)
	}
	var got InvokeArgs
	if err := decodeInvokeArgs(encoded, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Message != "hello, peer" {
		t.Fatalf("Message = %q", got.Message)
	}
}

// mustEncodeError is the test helper that builds an ERROR frame payload
// the same way the inbound side would.
func mustEncodeError(code int, msg string) []byte {
	b, err := encodeError(code, msg)
	if err != nil {
		panic(err)
	}
	return b
}
