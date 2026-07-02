package serve

import (
	"context"
	"testing"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
	vega "github.com/everydev1618/govega"
)

// wrappedStore embeds the Store interface so it satisfies Store but is NOT a
// *SQLiteStore. It stands in for a Postgres or otherwise-injected backend and
// reproduces the C2 crash: wireCallbacks used to type-assert s.store to
// *SQLiteStore, which panics for any non-SQLite store on process completion.
type wrappedStore struct {
	Store
}

// wireTestLLM is a no-op LLM so the orchestrator can spawn a process.
type wireTestLLM struct{}

func (wireTestLLM) Generate(context.Context, []llm.Message, []llm.ToolSchema) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: "ok"}, nil
}

func (wireTestLLM) GenerateStream(context.Context, []llm.Message, []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// TestWireCallbacks_NonSQLiteStoreDoesNotPanic ensures process completion and
// failure snapshot through the Store interface rather than asserting a concrete
// *SQLiteStore. Regression test for C2.
func TestWireCallbacks_NonSQLiteStoreDoesNotPanic(t *testing.T) {
	sqlite := newTestStore(t)
	doc := &dsl.Document{
		Agents:   map[string]*dsl.Agent{"worker": {Name: "worker", Model: "claude-sonnet-4-6"}},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}

	s := &Server{
		store:   wrappedStore{sqlite}, // not a *SQLiteStore
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
		cfg: Config{
			Builder:      dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{Name: "iris"},
		},
	}

	s.wireCallbacks()

	orch := interp.Orchestrator()
	agent := vega.Agent{Name: "worker", LLM: wireTestLLM{}}

	// Completion callback must not panic and must persist a snapshot.
	p1, err := orch.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p1.Complete("done")

	// Failure callback must not panic either.
	p2, err := orch.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p2.Fail(context.Canceled)

	snaps, err := sqlite.ListProcessSnapshots()
	if err != nil {
		t.Fatalf("ListProcessSnapshots: %v", err)
	}
	if len(snaps) < 2 {
		t.Errorf("got %d snapshots, want >= 2 (one per completed/failed process)", len(snaps))
	}
}
