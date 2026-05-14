package peering

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOp captures everything an inbound handler does so tests can assert on it.
type fakeOp struct {
	mu      sync.Mutex
	opID    uint64
	chunks  [][]byte
	errMsgs []errSent
	closed  bool
}

type errSent struct {
	Code    int
	Message string
}

func (f *fakeOp) OpID() uint64 { return f.opID }
func (f *fakeOp) SendChunk(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks = append(f.chunks, append([]byte(nil), p...))
	return nil
}
func (f *fakeOp) SendError(code int, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errMsgs = append(f.errMsgs, errSent{Code: code, Message: message})
	return nil
}
func (f *fakeOp) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// fakeDispatcher mimics Vega's StreamToAgent: emit chunks as if the agent
// were streaming tokens, then return final stats.
type fakeDispatcher struct {
	chunks []string
	stats  DispatchStats
	err    error

	gotAgent   string
	gotMessage string
	gotCtx     context.Context
}

func (d *fakeDispatcher) Dispatch(ctx context.Context, agent, message string, emit func([]byte) error) (DispatchStats, error) {
	d.gotAgent = agent
	d.gotMessage = message
	d.gotCtx = ctx
	for _, c := range d.chunks {
		if err := emit([]byte(c)); err != nil {
			return DispatchStats{}, err
		}
	}
	return d.stats, d.err
}

// --- helpers ---

func grantedSetup(t *testing.T) (Store, Peer) {
	t.Helper()
	s := newTestStore(t)
	p := samplePeer()
	if err := s.UpsertPeer(p); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGrant(sampleGrant(p.NodeID)); err != nil {
		t.Fatal(err)
	}
	return s, p
}

func encodeArgs(t *testing.T, message string) []byte {
	t.Helper()
	b, err := json.Marshal(InvokeArgs{Message: message})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- tests ---

func TestHandleInbound_HappyPath(t *testing.T) {
	s, p := grantedSetup(t)
	op := &fakeOp{opID: 42}
	disp := &fakeDispatcher{
		chunks: []string{"hello, ", "world"},
		stats:  DispatchStats{TokensIn: 12, TokensOut: 34, CostUSD: 0.001},
	}

	err := HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"researcher", encodeArgs(t, "hi"),
		op, s, disp)
	if err != nil {
		t.Fatalf("HandleInbound: %v", err)
	}

	if disp.gotAgent != "researcher" || disp.gotMessage != "hi" {
		t.Fatalf("dispatcher saw agent=%q message=%q", disp.gotAgent, disp.gotMessage)
	}
	if len(op.chunks) != 2 || string(op.chunks[0]) != "hello, " || string(op.chunks[1]) != "world" {
		t.Fatalf("chunks not streamed: %v", op.chunks)
	}
	if !op.closed {
		t.Fatal("op should be closed after success")
	}
	if len(op.errMsgs) != 0 {
		t.Fatalf("unexpected error frames: %v", op.errMsgs)
	}

	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if rows[0].Status != AuditStatusOK || rows[0].TokensOut != 34 || rows[0].OpID != 42 {
		t.Fatalf("audit row not finalized as OK: %+v", rows[0])
	}
}

func TestHandleInbound_UnknownPeerDeniesAndAudits(t *testing.T) {
	s := newTestStore(t)
	op := &fakeOp{opID: 1}
	disp := &fakeDispatcher{}

	err := HandleInbound(context.Background(),
		"vega:nobody", "",
		"researcher", encodeArgs(t, "hi"),
		op, s, disp)
	if err != nil {
		t.Fatalf("HandleInbound: %v", err)
	}
	if disp.gotAgent != "" {
		t.Fatal("dispatcher should not be called on denial")
	}
	if len(op.errMsgs) != 1 {
		t.Fatalf("expected one error frame, got %v", op.errMsgs)
	}
	if !strings.Contains(op.errMsgs[0].Message, "unknown") {
		t.Fatalf("error frame should explain denial; got %q", op.errMsgs[0].Message)
	}
	if !op.closed {
		t.Fatal("op should be closed even on denial")
	}

	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if len(rows) != 1 || rows[0].Status != AuditStatusDenied {
		t.Fatalf("expected one denied audit row, got %+v", rows)
	}
	if rows[0].DenialReason == "" {
		t.Fatal("denial row should carry DenialReason")
	}
}

func TestHandleInbound_NoGrantDenies(t *testing.T) {
	s := newTestStore(t)
	p := samplePeer()
	_ = s.UpsertPeer(p)
	op := &fakeOp{opID: 7}
	disp := &fakeDispatcher{}

	_ = HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"researcher", encodeArgs(t, "hi"),
		op, s, disp)

	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if len(rows) != 1 || rows[0].Status != AuditStatusDenied {
		t.Fatalf("expected denied audit row; got %+v", rows)
	}
	if disp.gotAgent != "" {
		t.Fatal("dispatcher should not run when grant is missing")
	}
}

func TestHandleInbound_MalformedArgs(t *testing.T) {
	s, p := grantedSetup(t)
	op := &fakeOp{opID: 1}
	disp := &fakeDispatcher{}

	err := HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"researcher", []byte("{not valid json"),
		op, s, disp)
	if err != nil {
		t.Fatalf("HandleInbound should swallow + audit, not bubble: %v", err)
	}
	if len(op.errMsgs) != 1 {
		t.Fatalf("expected one error frame, got %v", op.errMsgs)
	}
	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if len(rows) != 1 || rows[0].Status != AuditStatusError {
		t.Fatalf("expected one error audit row; got %+v", rows)
	}
}

func TestHandleInbound_EmptyMessageRejected(t *testing.T) {
	s, p := grantedSetup(t)
	op := &fakeOp{opID: 1}
	disp := &fakeDispatcher{}

	_ = HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"researcher", encodeArgs(t, ""),
		op, s, disp)
	if disp.gotAgent != "" {
		t.Fatal("dispatcher should not run on empty message")
	}
	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if len(rows) != 1 || rows[0].Status != AuditStatusError {
		t.Fatalf("expected one error audit row; got %+v", rows)
	}
}

func TestHandleInbound_DispatcherErrorAuditedAsError(t *testing.T) {
	s, p := grantedSetup(t)
	op := &fakeOp{opID: 9}
	disp := &fakeDispatcher{err: errors.New("agent exploded")}

	_ = HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"researcher", encodeArgs(t, "hi"),
		op, s, disp)

	if len(op.errMsgs) != 1 || !strings.Contains(op.errMsgs[0].Message, "exploded") {
		t.Fatalf("expected error frame surfacing dispatcher error; got %v", op.errMsgs)
	}
	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if len(rows) != 1 || rows[0].Status != AuditStatusError {
		t.Fatalf("expected one error audit row; got %+v", rows)
	}
}

func TestHandleInbound_DurationRecorded(t *testing.T) {
	s, p := grantedSetup(t)
	op := &fakeOp{opID: 1}
	disp := &fakeDispatcher{chunks: []string{"x"}}

	start := time.Now()
	_ = HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"researcher", encodeArgs(t, "hi"),
		op, s, disp)
	elapsed := time.Since(start)

	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if rows[0].DurationMS < 0 || time.Duration(rows[0].DurationMS)*time.Millisecond > elapsed+time.Second {
		t.Fatalf("DurationMS = %d; out of plausible range (elapsed=%v)", rows[0].DurationMS, elapsed)
	}
}

func TestHandleInbound_AgentCanonicalized(t *testing.T) {
	s, p := grantedSetup(t)
	op := &fakeOp{opID: 1}
	disp := &fakeDispatcher{chunks: []string{"ok"}}

	_ = HandleInbound(context.Background(),
		p.NodeID, p.Handle,
		"Researcher:proc-12345", encodeArgs(t, "hi"),
		op, s, disp)

	if disp.gotAgent != "researcher" {
		t.Fatalf("dispatcher saw agent=%q, want canonicalized 'researcher'", disp.gotAgent)
	}
	rows, _ := s.ListAudit(AuditFilter{Limit: 10})
	if rows[0].Status != AuditStatusOK {
		t.Fatalf("expected OK audit, got %+v", rows[0])
	}
	if rows[0].Agent != "researcher" {
		t.Fatalf("audit should record canonicalized agent name; got %q", rows[0].Agent)
	}
}
