package serve

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/events"
	"github.com/robfig/cron/v3"
)

// inboxChecker is a minimal interface for checking pending inbox items.
type inboxChecker interface {
	PendingInboxCount() (int, error)
}

// scheduleRunRecorder is the slice of the store the scheduler uses to
// stamp last_run_at after each fire (refs govega#52). Kept narrow so
// tests can stub it.
type scheduleRunRecorder interface {
	MarkScheduledJobRun(name string, at time.Time) error
}

// Scheduler runs cron jobs that send messages to agents.
// It implements dsl.SchedulerBackend.
type Scheduler struct {
	c        *cron.Cron
	interp   *dsl.Interpreter
	inbox    inboxChecker         // optional — used to skip no-op heartbeats
	recorder scheduleRunRecorder // optional — stamps last_run_at after each fire
	persist  func(job dsl.ScheduledJob) error
	remove   func(name string) error
	resolver CallerResolver // optional — enriches per-fire ctx with caller identity (apexvega#24)

	mu      sync.Mutex
	jobs    []dsl.ScheduledJob
	entries map[string]cron.EntryID // job name → cron entry ID
}

// SetCallerResolver registers a CallerResolver that runs at every job
// fire to enrich the dispatch context with the scheduling user's
// identity and per-user credentials. Pass nil to clear.
func (s *Scheduler) SetCallerResolver(r CallerResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolver = r
}

// fireContext builds the context passed to SendToAgent for a job fire.
// Extracted from makeFunc so the resolver wiring is unit-testable
// without needing a live cron or interpreter.
func (s *Scheduler) fireContext(job dsl.ScheduledJob) context.Context {
	_ = job // reserved: future commits will read job.CreatedBy and pass it to resolver
	s.mu.Lock()
	r := s.resolver
	s.mu.Unlock()
	return applyResolver(context.Background(), r, "")
}

// NewScheduler creates a Scheduler. The persist and remove callbacks are
// called after successfully adding/removing a job so it can be saved to
// permanent storage. Either may be nil if persistence is not needed.
func NewScheduler(
	interp *dsl.Interpreter,
	persist func(job dsl.ScheduledJob) error,
	remove func(name string) error,
) *Scheduler {
	return &Scheduler{
		c:       cron.New(),
		interp:  interp,
		persist: persist,
		remove:  remove,
		entries: make(map[string]cron.EntryID),
	}
}

// Start begins the cron runner and blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	s.c.Start()
	slog.Info("scheduler started")
	<-ctx.Done()
	s.c.Stop()
	slog.Info("scheduler stopped")
}

// AddJob adds a job to the cron runner and persists it.
// If a job with the same name already exists it is replaced.
func (s *Scheduler) AddJob(job dsl.ScheduledJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If a job with this name exists, remove it first.
	if id, ok := s.entries[job.Name]; ok {
		s.c.Remove(id)
		delete(s.entries, job.Name)
		s.jobs = removeJobByName(s.jobs, job.Name)
	}

	if !job.Enabled {
		// Still persist the disabled job so it can be restored later.
		s.jobs = append(s.jobs, job)
		if s.persist != nil && !job.InMemoryOnly {
			if err := s.persist(job); err != nil {
				slog.Warn("scheduler: persist job failed", "name", job.Name, "error", err)
			}
		}
		return nil
	}

	entryID, err := s.c.AddFunc(job.Cron, s.makeFunc(job))
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", job.Cron, err)
	}

	s.entries[job.Name] = entryID
	s.jobs = append(s.jobs, job)

	if s.persist != nil && !job.InMemoryOnly {
		if err := s.persist(job); err != nil {
			slog.Warn("scheduler: persist job failed", "name", job.Name, "error", err)
		}
	}

	slog.Info("scheduler: job added", "name", job.Name, "cron", job.Cron, "agent", job.AgentName)
	return nil
}

// RemoveJob removes a job from the cron runner and calls the remove callback.
func (s *Scheduler) RemoveJob(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.entries[name]
	if !ok {
		// May exist as a disabled job (no cron entry).
		found := false
		for _, j := range s.jobs {
			if j.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("schedule %q not found", name)
		}
	} else {
		s.c.Remove(id)
		delete(s.entries, name)
	}

	s.jobs = removeJobByName(s.jobs, name)

	if s.remove != nil {
		if err := s.remove(name); err != nil {
			slog.Warn("scheduler: remove job from store failed", "name", name, "error", err)
		}
	}

	slog.Info("scheduler: job removed", "name", name)
	return nil
}

// ListJobs returns a snapshot of all current jobs.
func (s *Scheduler) ListJobs() []dsl.ScheduledJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]dsl.ScheduledJob, len(s.jobs))
	copy(out, s.jobs)
	return out
}

// makeFunc returns the cron callback for a job.
func (s *Scheduler) makeFunc(job dsl.ScheduledJob) func() {
	// Overlap guard: a firing that arrives while the previous run of this
	// job is still executing is skipped, not queued. A slow agent turn
	// (or a wedged tool) must not stack a backlog of cron firings that
	// all replay the same message when the agent recovers.
	var inFlight atomic.Bool
	return func() {
		if !inFlight.CompareAndSwap(false, true) {
			slog.Warn("scheduler: skipping firing — previous run still in flight", "name", job.Name)
			return
		}
		defer inFlight.Store(false)

		// For heartbeat jobs, skip the LLM call entirely if the inbox
		// is empty — saves tokens when the system is idle. Matches any
		// "*-heartbeat" so tenants with renamed orchestrators (knox,
		// wilfred, …) keep the optimization (govega#101).
		if s.inbox != nil && strings.HasSuffix(job.Name, "-heartbeat") {
			count, err := s.inbox.PendingInboxCount()
			if err == nil && count == 0 {
				slog.Debug("scheduler: skipping heartbeat — inbox empty", "name", job.Name)
				return
			}
		}

		slog.Info("scheduler: firing job", "name", job.Name, "agent", job.AgentName)

		// Emit onto the reactive spine so agents can wake on the clock
		// (schedule.fired) — the job's own AgentName still runs below.
		s.interp.PublishEvent(events.Event{
			Type: "schedule.fired",
			Data: map[string]any{"job": job.Name, "agent": job.AgentName},
		})

		ctx := s.fireContext(job)
		// Use SendToAgent (synchronous, no inbox item) instead of
		// DispatchToAgent to avoid spamming the inbox with no-op
		// heartbeat results like "inbox empty."
		if _, err := s.interp.SendToAgent(ctx, job.AgentName, job.Message); err != nil {
			slog.Warn("scheduler: agent send failed", "name", job.Name, "agent", job.AgentName, "error", err)
		}
		// Stamp last_run_at so the FE's Routines list reflects "last fired"
		// (refs govega#52). Best-effort — a stamp failure is not a real
		// failure of the job itself.
		if s.recorder != nil {
			if err := s.recorder.MarkScheduledJobRun(job.Name, time.Now().UTC()); err != nil {
				slog.Warn("scheduler: stamp last_run_at failed", "name", job.Name, "error", err)
			}
		}
	}
}

func removeJobByName(jobs []dsl.ScheduledJob, name string) []dsl.ScheduledJob {
	out := jobs[:0]
	for _, j := range jobs {
		if j.Name != name {
			out = append(out, j)
		}
	}
	return out
}

// heartbeatStore is the narrow slice of the store the prune step needs.
// Kept as an interface so the helper is trivially unit-testable.
type heartbeatStore interface {
	ListScheduledJobs() ([]ScheduledJob, error)
	DeleteScheduledJob(name string) error
}

// pruneStaleOrchestratorHeartbeats deletes any persisted job whose name
// matches the auto-generated "<agent>-heartbeat" pattern (i.e. Name ==
// AgentName+"-heartbeat"). The orchestrator heartbeat is now added as an
// InMemoryOnly job on each boot, so any such row in the store is by
// definition a leftover from before govega#101 (typically from a tenant
// orchestrator rename). User-created routines don't follow that exact
// pattern and are preserved.
//
// Returns the number of rows pruned. Errors on individual deletes are
// logged and counted as failures (the prune continues on the rest).
func pruneStaleOrchestratorHeartbeats(store heartbeatStore) (int, error) {
	jobs, err := store.ListScheduledJobs()
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, j := range jobs {
		if !strings.HasSuffix(j.Name, "-heartbeat") {
			continue
		}
		if j.AgentName+"-heartbeat" != j.Name {
			continue
		}
		if err := store.DeleteScheduledJob(j.Name); err != nil {
			slog.Warn("scheduler: prune legacy heartbeat failed", "name", j.Name, "error", err)
			continue
		}
		slog.Info("scheduler: pruned legacy orchestrator heartbeat", "name", j.Name)
		pruned++
	}
	return pruned, nil
}
