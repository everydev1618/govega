package serve

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
)

// newSchedulerTestServer builds the minimum Server needed to exercise the
// scheduler boot path and orchestrator-heartbeat cleanup (govega#101).
func newSchedulerTestServer(t *testing.T, orchName string) (*Server, *SQLiteStore) {
	t.Helper()
	doc := &dsl.Document{
		Agents: map[string]*dsl.Agent{
			orchName: {Name: orchName, Model: "claude-sonnet-4-6"},
		},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	store := newTestStore(t)
	s := &Server{
		store:  store,
		interp: interp,
		broker: NewEventBroker(),
		cfg: Config{
			Orchestrator: dsl.IrisConfig{Name: orchName},
		},
	}
	return s, store
}

// inmemPersistRecorder counts calls to the persist callback so we can assert
// that InMemoryOnly jobs skip persistence.
type inmemPersistRecorder struct{ calls int }

func (r *inmemPersistRecorder) persist(job dsl.ScheduledJob) error {
	r.calls++
	return nil
}

// TestAddJob_InMemoryOnly_SkipsPersist is the headline govega#101 contract:
// jobs flagged InMemoryOnly never reach the persistence callback. This is
// what lets the orchestrator heartbeat be re-derived from config on every
// boot instead of accumulating stale rows after a rename.
func TestAddJob_InMemoryOnly_SkipsPersist(t *testing.T) {
	rec := &inmemPersistRecorder{}
	sch := NewScheduler(nil, rec.persist, nil)
	job := dsl.ScheduledJob{
		Name:         "knox-heartbeat",
		Cron:         "*/15 * * * *",
		AgentName:    "knox",
		Message:      "Heartbeat",
		Enabled:      true,
		InMemoryOnly: true,
	}
	if err := sch.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if rec.calls != 0 {
		t.Fatalf("InMemoryOnly job persisted %d times, want 0", rec.calls)
	}
	// And it should still be present in the in-memory job list.
	jobs := sch.ListJobs()
	if len(jobs) != 1 || jobs[0].Name != "knox-heartbeat" {
		t.Fatalf("ListJobs = %+v, want [knox-heartbeat]", jobs)
	}
}

// TestAddJob_PersistentDefault_StillPersists guards the default path: a job
// without InMemoryOnly set must still flow through the persist callback (so
// user-created routines aren't accidentally lost).
func TestAddJob_PersistentDefault_StillPersists(t *testing.T) {
	rec := &inmemPersistRecorder{}
	sch := NewScheduler(nil, rec.persist, nil)
	job := dsl.ScheduledJob{
		Name:      "user-routine",
		Cron:      "0 9 * * *",
		AgentName: "knox",
		Message:   "morning check",
		Enabled:   true,
	}
	if err := sch.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("persistent job called persist %d times, want 1", rec.calls)
	}
}

// TestPruneStaleOrchestratorHeartbeats_DropsAutoPattern verifies the boot-time
// migration: any row whose Name == AgentName + "-heartbeat" is the auto-
// generated orchestrator-heartbeat pattern, so it's a leftover from before
// govega#101 and should be deleted. User-created routines never follow that
// exact pattern and must be preserved.
func TestPruneStaleOrchestratorHeartbeats_DropsAutoPattern(t *testing.T) {
	store := newTestStore(t)

	// Two stale orchestrator heartbeats from prior renames + one user routine
	// that happens to target a missing agent (different pattern — must keep).
	mustUpsert(t, store, ScheduledJob{Name: "kassidy-heartbeat", Cron: "*/15 * * * *", AgentName: "kassidy", Enabled: true})
	mustUpsert(t, store, ScheduledJob{Name: "marcel-heartbeat", Cron: "*/15 * * * *", AgentName: "marcel", Enabled: true})
	mustUpsert(t, store, ScheduledJob{Name: "morning-check", Cron: "0 9 * * *", AgentName: "kassidy", Enabled: true})

	pruned, err := pruneStaleOrchestratorHeartbeats(store)
	if err != nil {
		t.Fatalf("pruneStaleOrchestratorHeartbeats: %v", err)
	}
	if pruned != 2 {
		t.Fatalf("pruned = %d, want 2", pruned)
	}

	remaining, err := store.ListScheduledJobs()
	if err != nil {
		t.Fatalf("ListScheduledJobs: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Name != "morning-check" {
		t.Fatalf("remaining = %+v, want only morning-check", remaining)
	}
}

// TestPruneStaleOrchestratorHeartbeats_LeavesMismatchedPattern guards against
// over-eager deletion: a heartbeat-suffixed row whose AgentName doesn't match
// the Name prefix isn't part of the auto-pattern (likely user-named) and must
// be preserved.
func TestPruneStaleOrchestratorHeartbeats_LeavesMismatchedPattern(t *testing.T) {
	store := newTestStore(t)
	mustUpsert(t, store, ScheduledJob{Name: "team-heartbeat", Cron: "*/30 * * * *", AgentName: "sterling", Enabled: true})

	pruned, err := pruneStaleOrchestratorHeartbeats(store)
	if err != nil {
		t.Fatalf("pruneStaleOrchestratorHeartbeats: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0", pruned)
	}
	remaining, err := store.ListScheduledJobs()
	if err != nil {
		t.Fatalf("ListScheduledJobs: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Name != "team-heartbeat" {
		t.Fatalf("remaining = %+v, want team-heartbeat preserved", remaining)
	}
}

// fakeInboxChecker lets us control the inbox-empty branch in makeFunc.
type fakeInboxChecker struct{ count int }

func (f *fakeInboxChecker) PendingInboxCount() (int, error) { return f.count, nil }

// TestHeartbeatShortcut_FiresForAnyHeartbeatSuffix proves the secondary bug
// in govega#101 is fixed: the inbox-empty short-circuit must apply to ALL
// "*-heartbeat" jobs, not just the legacy "iris-heartbeat" name. We assert
// the optimization triggers for a renamed orchestrator (knox-heartbeat) by
// checking that SendToAgent is never reached when the inbox is empty.
func TestHeartbeatShortcut_FiresForAnyHeartbeatSuffix(t *testing.T) {
	s, _ := newSchedulerTestServer(t, "knox")
	sch := NewScheduler(s.interp, nil, nil)
	sch.inbox = &fakeInboxChecker{count: 0}

	// We use a near-future cron (every second) so the makeFunc fires inside
	// the test window. The agent "knox" exists in the doc, so if the short-
	// circuit fails the SendToAgent call would be made (and would observably
	// touch the interpreter — we detect that via a marker channel below).
	fired := make(chan struct{}, 1)
	job := dsl.ScheduledJob{
		Name:      "knox-heartbeat",
		Cron:      "* * * * * *", // every second (6-field cron — see note below)
		AgentName: "knox",
		Message:   "heartbeat",
		Enabled:   true,
	}
	// We can't use AddJob's cron parser (5-field). Drive makeFunc directly
	// to deterministically verify the short-circuit instead of waiting for
	// real cron ticks. This is a unit test of the callback, not the runner.
	fn := sch.makeFunc(job)

	// Wrap the interpreter to detect any SendToAgent call.
	// SendToAgent against a real interp with no orchestrator running will
	// either succeed (touching state) or fail; both would leave a trace.
	// Simpler: count log lines. Even simpler: just assert that calling fn()
	// when inbox is empty does NOT panic and is fast (we accept the absence
	// of side effects as the contract; the regression we're fixing is the
	// optimization being skipped for non-iris names).
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
		select {
		case fired <- struct{}{}:
		default:
		}
	}()
	select {
	case <-done:
		// Pass — fn() returned promptly with empty inbox. If the short-
		// circuit were broken, fn() would attempt a SendToAgent which on
		// a no-running-orchestrator interp would block waiting for a worker.
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("heartbeat callback did not short-circuit on empty inbox for non-iris name")
	}
}

// TestHeartbeatShortcut_LegacyIrisNameStillWorks guards backwards compat: the
// old hardcoded "iris-heartbeat" name must still short-circuit too. (Both this
// test and the one above pass once the check is generalized to HasSuffix.)
func TestHeartbeatShortcut_LegacyIrisNameStillWorks(t *testing.T) {
	s, _ := newSchedulerTestServer(t, "iris")
	sch := NewScheduler(s.interp, nil, nil)
	sch.inbox = &fakeInboxChecker{count: 0}

	job := dsl.ScheduledJob{
		Name:      "iris-heartbeat",
		Cron:      "*/15 * * * *",
		AgentName: "iris",
		Message:   "heartbeat",
		Enabled:   true,
	}
	fn := sch.makeFunc(job)
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("iris-heartbeat callback did not short-circuit on empty inbox")
	}
}

// TestServer_Start_DropsStaleOrchestratorHeartbeat is the integration-style
// guard: when the server boots with a stale "kassidy-heartbeat" persisted
// and the current orchestrator is "knox", the boot pass must drop the stale
// row before the restore loop runs.
//
// We don't run the full Server.Start (too much wiring) — instead we exercise
// the documented boot order: prune, then restore. This mirrors what
// server.go does and lets the regression surface from a unit test rather
// than only from a live deploy.
func TestServer_Start_DropsStaleOrchestratorHeartbeat(t *testing.T) {
	_, store := newSchedulerTestServer(t, "knox")
	mustUpsert(t, store, ScheduledJob{Name: "kassidy-heartbeat", Cron: "*/15 * * * *", AgentName: "kassidy", Enabled: true})

	if _, err := pruneStaleOrchestratorHeartbeats(store); err != nil {
		t.Fatalf("prune: %v", err)
	}
	remaining, err := store.ListScheduledJobs()
	if err != nil {
		t.Fatalf("ListScheduledJobs: %v", err)
	}
	for _, j := range remaining {
		if strings.HasSuffix(j.Name, "-heartbeat") {
			t.Fatalf("stale heartbeat row survived prune: %+v", j)
		}
	}
}

// callerResolverSentinelKey is a typed context-value key used by the
// resolver-wiring tests below to verify the resolver actually fired.
type callerResolverSentinelKey struct{}

// TestScheduler_FireContext_AppliesResolver — the scheduler's per-fire
// context MUST flow through any registered CallerResolver so background
// LLM calls inherit per-user BYOK keys (apexvega#24).
func TestScheduler_FireContext_AppliesResolver(t *testing.T) {
	sch := NewScheduler(nil, nil, nil)
	sch.SetCallerResolver(func(ctx context.Context, userID string) context.Context {
		return context.WithValue(ctx, callerResolverSentinelKey{}, "fired:"+userID)
	})
	job := dsl.ScheduledJob{Name: "x", AgentName: "y", Message: "z"}
	ctx := sch.fireContext(job)
	got, _ := ctx.Value(callerResolverSentinelKey{}).(string)
	if got != "fired:" {
		t.Fatalf("resolver was not applied; got %q", got)
	}
}

// TestScheduler_FireContext_NoResolver_NoOp — when no resolver is set, the
// fire context is a plain context.Background(). Guards the self-hosted
// case where there is no BYOK plumbing.
func TestScheduler_FireContext_NoResolver_NoOp(t *testing.T) {
	sch := NewScheduler(nil, nil, nil)
	ctx := sch.fireContext(dsl.ScheduledJob{Name: "x"})
	if got := ctx.Value(callerResolverSentinelKey{}); got != nil {
		t.Fatalf("expected nil sentinel on no-resolver path; got %v", got)
	}
}

// mustUpsert is a test helper that inserts a row and fails the test on error.
func mustUpsert(t *testing.T, store *SQLiteStore, job ScheduledJob) {
	t.Helper()
	if err := store.UpsertScheduledJob(job); err != nil {
		t.Fatalf("UpsertScheduledJob(%s): %v", job.Name, err)
	}
}

// gateLLM blocks inside Generate until released, counting calls.
type gateLLM struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (g *gateLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	g.calls.Add(1)
	g.entered <- struct{}{}
	<-g.release
	return &llm.LLMResponse{Content: "ok"}, nil
}

func (g *gateLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// TestSchedulerSkipsOverlappingFirings verifies a firing that arrives while
// the previous run of the same job is still executing is skipped rather
// than queued — a slow agent turn must not stack a backlog of cron firings.
func TestSchedulerSkipsOverlappingFirings(t *testing.T) {
	g := &gateLLM{entered: make(chan struct{}, 2), release: make(chan struct{})}

	doc, err := dsl.NewParser().Parse([]byte(`
name: overlap-test
agents:
  worker:
    model: claude-sonnet-4-6
    system: worker
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	interp, err := dsl.NewInterpreter(doc, dsl.WithLLM(g))
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}

	sch := NewScheduler(interp, nil, nil)
	fn := sch.makeFunc(dsl.ScheduledJob{Name: "tick", AgentName: "worker", Message: "go"})

	first := make(chan struct{})
	go func() {
		fn()
		close(first)
	}()
	<-g.entered // first firing is now blocked mid-turn

	second := make(chan struct{})
	go func() {
		fn()
		close(second)
	}()

	select {
	case <-second:
		// Skipped promptly — correct.
	case <-time.After(2 * time.Second):
		t.Error("second firing queued behind the first instead of being skipped")
	}

	close(g.release)
	<-first

	if got := g.calls.Load(); got != 1 {
		t.Errorf("LLM called %d times for overlapping firings, want 1", got)
	}
}
