package serve

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/everydev1618/govega/dsl"
	"github.com/robfig/cron/v3"
)

// Scheduler runs cron jobs that send messages to agents.
// It implements dsl.SchedulerBackend.
type Scheduler struct {
	c       *cron.Cron
	interp  *dsl.Interpreter
	persist func(job dsl.ScheduledJob) error
	remove  func(name string) error

	mu      sync.Mutex
	jobs    []dsl.ScheduledJob
	entries map[string]cron.EntryID // job name → cron entry ID
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
		if s.persist != nil {
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

	if s.persist != nil {
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
	return func() {
		slog.Info("scheduler: firing job", "name", job.Name, "agent", job.AgentName)
		ctx := context.Background()
		if _, err := s.interp.SendToAgent(ctx, job.AgentName, job.Message); err != nil {
			slog.Warn("scheduler: agent call failed", "name", job.Name, "agent", job.AgentName, "error", err)
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
