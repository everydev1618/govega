package vega

import (
	"context"
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
