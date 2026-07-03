package vega

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
)

// blockingLLM blocks inside Generate/GenerateStream until its ctx is cancelled,
// signaling once it has entered the call.
type blockingLLM struct{ entered chan struct{} }

func (b *blockingLLM) Generate(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingLLM) GenerateStream(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// TestStopAbortsInFlightSend verifies Stop() cancels an in-flight Send even
// when the caller passed a non-cancellable context. Regression test for H2:
// the LLM loop must observe p.ctx, not just the caller ctx.
func TestStopAbortsInFlightSend(t *testing.T) {
	ll := &blockingLLM{entered: make(chan struct{}, 1)}
	o := NewOrchestrator(WithLLM(ll))
	p, err := o.Spawn(Agent{Name: "x"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	errc := make(chan error, 1)
	go func() {
		// Background caller ctx never cancels — only p.ctx can end this.
		_, e := p.Send(context.Background(), "hi")
		errc <- e
	}()

	<-ll.entered // ensure we're blocked inside the LLM call
	p.Stop()     // cancels p.ctx

	select {
	case e := <-errc:
		if e == nil {
			t.Error("Send returned nil error after Stop, want a cancellation error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send did not abort after Stop — p.ctx not observed by the LLM loop (H2)")
	}
}

// TestTurnTimesOutOnStalledLLMWithoutDeadline verifies a turn on a
// non-cancellable, no-deadline context (exactly what the Discord/Telegram bots
// pass — context.Background()) still terminates on the per-turn timeout instead
// of hanging forever. Regression test for the "TonyVega is typing… forever"
// hang: a stalled LLM stream or tool call, with nobody to Stop the process,
// must self-abort on the turn deadline.
func TestTurnTimesOutOnStalledLLMWithoutDeadline(t *testing.T) {
	ll := &blockingLLM{entered: make(chan struct{}, 1)}
	o := NewOrchestrator(WithLLM(ll))
	p, err := o.Spawn(Agent{Name: "x"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p.SetTurnTimeout(150 * time.Millisecond)

	errc := make(chan error, 1)
	go func() {
		// Background caller ctx never cancels and has no deadline; nobody
		// calls Stop. Only the per-turn timeout can end this.
		_, e := p.Send(context.Background(), "hi")
		errc <- e
	}()

	select {
	case e := <-errc:
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatalf("Send returned %v, want context.DeadlineExceeded", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send hung on a no-deadline context — per-turn timeout not applied (infinite-typing bug)")
	}
}

// chattyLLM streams many deltas then finishes, letting a test fill the stream
// buffer without ever draining it.
type chattyLLM struct{}

func (chattyLLM) Generate(context.Context, []llm.Message, []llm.ToolSchema) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: "done"}, nil
}

func (chattyLLM) GenerateStream(ctx context.Context, _ []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		defer close(ch)
		for i := 0; i < 100000; i++ {
			select {
			case ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: "x"}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// TestAbandonedStreamUnblocksOnCancel verifies the producer goroutine doesn't
// block forever when the consumer stops reading: cancelling the process must
// unblock the send. Regression test for H6.
func TestAbandonedStreamUnblocksOnCancel(t *testing.T) {
	o := NewOrchestrator(WithLLM(chattyLLM{}))
	p, err := o.Spawn(Agent{Name: "x"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	stream, err := p.SendStream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}

	// Deliberately do NOT read stream.chunks. Once the buffer fills, the
	// producer blocks on the send. Cancelling the process must free it.
	time.Sleep(50 * time.Millisecond)
	p.Stop()

	select {
	case <-stream.done:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned stream producer did not unblock after Stop (H6)")
	}
}

// TestKillAbortsInFlightStream verifies orchestrator Kill aborts an in-flight
// streaming Send.
func TestKillAbortsInFlightStream(t *testing.T) {
	ll := &blockingLLM{entered: make(chan struct{}, 1)}
	o := NewOrchestrator(WithLLM(ll))
	p, err := o.Spawn(Agent{Name: "x"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	stream, err := p.SendStream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}

	<-ll.entered
	_ = o.Kill(p.ID)

	select {
	case <-stream.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish after Kill — p.ctx not observed by the stream loop (H2)")
	}
}
