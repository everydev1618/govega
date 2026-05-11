package serve

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/everydev1618/govega/dsl"
)

// --- Per-agent Schedules (a.k.a. Routines) — refs govega#52 ---
//
// The legacy flat `/api/v1/schedules` endpoints stay (DSL-created jobs
// still flow through them). The new nested surface below is what the
// apex-host-mgmt Routines UI uses.

// handleListAgentRoutines returns every routine scoped to one agent.
// GET /api/v1/agents/{agent}/schedules
func (s *Server) handleListAgentRoutines(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	if agent == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "agent is required"})
		return
	}
	persisted, err := s.store.ListScheduledJobs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]AgentRoutineResponse, 0)
	for _, sj := range persisted {
		if sj.AgentName != agent {
			continue
		}
		out = append(out, routineToResponse(sj))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetAgentRoutine returns one routine by id.
// GET /api/v1/agents/{agent}/schedules/{id}
func (s *Server) handleGetAgentRoutine(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")
	job, err := s.store.GetScheduledJobByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if job == nil || job.AgentName != agent {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "routine not found"})
		return
	}
	writeJSON(w, http.StatusOK, routineToResponse(*job))
}

// handleCreateAgentRoutine creates a new routine for an agent. The id is
// server-generated; the FE supplies title, instructions, and a structured
// schedule.
// POST /api/v1/agents/{agent}/schedules
func (s *Server) handleCreateAgentRoutine(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	if agent == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "agent is required"})
		return
	}
	var req CreateRoutineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	if req.Title == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "title is required"})
		return
	}
	if req.Instructions == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "instructions is required"})
		return
	}
	if err := req.Schedule.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	cronExpr, err := req.Schedule.ToCron()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	id := newRoutineID()
	schedJSON, _ := json.Marshal(req.Schedule)

	// Persist the full row first so the new fields land in the DB; then
	// add to the cron runner (which calls our persist callback again with
	// only the legacy fields, but UpsertScheduledJob is idempotent and
	// preserves existing columns when id collides via the COALESCE).
	job := ScheduledJob{
		ID:           id,
		Name:         id, // new routines use id as the cron-runner key
		Title:        req.Title,
		Cron:         cronExpr,
		AgentName:    agent,
		Message:      req.Instructions,
		ScheduleJSON: string(schedJSON),
		Enabled:      enabled,
	}
	if err := s.store.UpsertScheduledJob(job); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	dslJob := dsl.ScheduledJob{
		Name:      id,
		Cron:      cronExpr,
		AgentName: agent,
		Message:   req.Instructions,
		Enabled:   enabled,
	}
	if err := s.scheduler.AddJob(dslJob); err != nil {
		// Roll back the DB row so the FE doesn't see a routine that
		// can't actually fire (e.g. invalid cron survived Validate).
		_ = s.store.DeleteScheduledJob(id)
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	// Re-read to pick up canonical created_at / updated_at.
	stored, _ := s.store.GetScheduledJobByID(id)
	if stored == nil {
		stored = &job
		stored.CreatedAt = time.Now().UTC()
		stored.UpdatedAt = stored.CreatedAt
	}
	writeJSON(w, http.StatusCreated, routineToResponse(*stored))
}

// handleUpdateAgentRoutine partially updates an existing routine.
// PATCH /api/v1/agents/{agent}/schedules/{id}
func (s *Server) handleUpdateAgentRoutine(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")
	job, err := s.store.GetScheduledJobByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if job == nil || job.AgentName != agent {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "routine not found"})
		return
	}
	var req UpdateRoutineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}
	if req.Title != nil {
		job.Title = *req.Title
	}
	if req.Instructions != nil {
		job.Message = *req.Instructions
	}
	if req.Schedule != nil {
		if err := req.Schedule.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}
		cronExpr, err := req.Schedule.ToCron()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}
		job.Cron = cronExpr
		b, _ := json.Marshal(req.Schedule)
		job.ScheduleJSON = string(b)
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}

	// Persist first, then re-add to the cron runner. AddJob with the same
	// name replaces any existing entry.
	if err := s.store.UpsertScheduledJob(*job); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	dslJob := dsl.ScheduledJob{
		Name:      job.Name,
		Cron:      job.Cron,
		AgentName: job.AgentName,
		Message:   job.Message,
		Enabled:   job.Enabled,
	}
	if err := s.scheduler.AddJob(dslJob); err != nil {
		slog.Warn("PATCH routine: scheduler re-add failed", "id", id, "error", err)
	}
	stored, _ := s.store.GetScheduledJobByID(id)
	if stored == nil {
		stored = job
	}
	writeJSON(w, http.StatusOK, routineToResponse(*stored))
}

// handleDeleteAgentRoutine removes a routine by id.
// DELETE /api/v1/agents/{agent}/schedules/{id}
func (s *Server) handleDeleteAgentRoutine(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")
	job, err := s.store.GetScheduledJobByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if job == nil || job.AgentName != agent {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "routine not found"})
		return
	}
	if err := s.scheduler.RemoveJob(job.Name); err != nil {
		// Already gone from the cron runner is fine — keep going so the
		// DB row gets cleaned up too.
		slog.Warn("DELETE routine: scheduler remove failed", "id", id, "error", err)
	}
	if err := s.store.DeleteScheduledJob(job.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// routineToResponse converts a persisted ScheduledJob into the API shape.
// For rows missing a structured Schedule (DSL-created or legacy), we
// synthesize a `Custom` schedule echoing the raw cron — the FE can render
// it read-only without parsing cron itself.
func routineToResponse(j ScheduledJob) AgentRoutineResponse {
	var sched Schedule
	if j.ScheduleJSON != "" {
		_ = json.Unmarshal([]byte(j.ScheduleJSON), &sched)
	}
	if sched.Frequency == "" {
		sched = Schedule{Frequency: FrequencyCustom, Cron: j.Cron}
	}
	title := j.Title
	if title == "" {
		title = j.Name
	}
	resp := AgentRoutineResponse{
		ID:           j.ID,
		Agent:        j.AgentName,
		Title:        title,
		Instructions: j.Message,
		Schedule:     sched,
		Cron:         j.Cron,
		Enabled:      j.Enabled,
		LastRunAt:    j.LastRunAt,
		NextRunAt:    NextRun(j.Cron, time.Now()),
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}
	if resp.ID == "" {
		// Legacy rows persisted before the id backfill migration ran —
		// fall back to name so the FE has a stable handle.
		resp.ID = j.Name
	}
	return resp
}
