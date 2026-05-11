package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everydev1618/govega/dsl"
)

// routineTestServer wires the minimum a *Server needs for routines
// handler tests: real Interpreter so spawnAgent runs, real store so
// the routines round-trip, and a real Scheduler so AddJob respects cron
// validation.
func routineTestServer(t *testing.T, agentName string) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents:   map[string]*dsl.Agent{agentName: {Name: agentName, Model: "claude-sonnet-4-6"}},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	store := newTestStore(t)
	s := &Server{
		store:   store,
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
		cfg: Config{
			Builder:      dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{Name: "iris"},
		},
	}
	s.scheduler = NewScheduler(
		interp,
		func(job dsl.ScheduledJob) error {
			row := ScheduledJob{
				Name: job.Name, Cron: job.Cron, AgentName: job.AgentName,
				Message: job.Message, Enabled: job.Enabled,
			}
			if existing, err := store.GetScheduledJobByID(job.Name); err == nil && existing != nil {
				row.ID = existing.ID
				row.Title = existing.Title
				row.ScheduleJSON = existing.ScheduleJSON
			}
			return store.UpsertScheduledJob(row)
		},
		store.DeleteScheduledJob,
	)
	s.scheduler.recorder = store
	return s
}

// TestCreateAgentRoutine_StructuredScheduleRoundTrip is the headline
// govega#52 contract: the FE POSTs a structured Schedule, the server
// converts to cron + persists both, and GETs return the same structured
// shape (plus the derived cron, last_run_at/next_run_at).
func TestCreateAgentRoutine_StructuredScheduleRoundTrip(t *testing.T) {
	s := routineTestServer(t, "riley")
	body := mustJSON(t, CreateRoutineRequest{
		Title:        "Daily standup reminder",
		Instructions: "Post the standup template to #general at 9am.",
		Schedule: Schedule{
			Frequency: FrequencyDaily,
			Time:      "09:00",
			Timezone:  "America/New_York",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/riley/schedules", bytes.NewReader(body))
	req.SetPathValue("agent", "riley")
	w := httptest.NewRecorder()
	s.handleCreateAgentRoutine(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentRoutineResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" {
		t.Error("id missing — should be server-generated")
	}
	if got.Title != "Daily standup reminder" {
		t.Errorf("title = %q, want Daily standup reminder", got.Title)
	}
	if got.Instructions != "Post the standup template to #general at 9am." {
		t.Errorf("instructions wrong: %q", got.Instructions)
	}
	if got.Schedule.Frequency != FrequencyDaily {
		t.Errorf("frequency = %q, want daily", got.Schedule.Frequency)
	}
	if got.Schedule.Time != "09:00" {
		t.Errorf("time = %q, want 09:00", got.Schedule.Time)
	}
	if got.Cron != "CRON_TZ=America/New_York 0 9 * * *" {
		t.Errorf("derived cron = %q, want CRON_TZ=America/New_York 0 9 * * *", got.Cron)
	}
	if got.NextRunAt == nil {
		t.Error("next_run_at missing — should be computed from cron")
	}
	if !got.Enabled {
		t.Error("enabled should default to true")
	}

	// GET by id returns the same shape.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/schedules/"+got.ID, nil)
	getReq.SetPathValue("agent", "riley")
	getReq.SetPathValue("id", got.ID)
	getW := httptest.NewRecorder()
	s.handleGetAgentRoutine(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getW.Code)
	}
	var got2 AgentRoutineResponse
	_ = json.NewDecoder(getW.Body).Decode(&got2)
	if got2.ID != got.ID || got2.Title != got.Title {
		t.Errorf("GET shape diverged from POST: %+v vs %+v", got2, got)
	}
}

// TestCreateAgentRoutine_RejectsInvalidSchedule confirms validation
// fires at the API boundary rather than being silently coerced.
func TestCreateAgentRoutine_RejectsInvalidSchedule(t *testing.T) {
	s := routineTestServer(t, "riley")
	body := mustJSON(t, CreateRoutineRequest{
		Title:        "Bad time",
		Instructions: "doesn't matter",
		Schedule:     Schedule{Frequency: FrequencyDaily, Time: "25:99", Timezone: "UTC"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/riley/schedules", bytes.NewReader(body))
	req.SetPathValue("agent", "riley")
	w := httptest.NewRecorder()
	s.handleCreateAgentRoutine(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

// TestListAgentRoutines_PerAgentScoping ensures a GET for one agent only
// returns that agent's routines — fixing the per-agent leak in the
// existing flat /api/v1/schedules surface.
func TestListAgentRoutines_PerAgentScoping(t *testing.T) {
	s := routineTestServer(t, "riley")
	// Also register alex so AddJob doesn't fail on agent-not-found
	// for the second routine.
	_ = s.interp.AddAgent("alex", &dsl.Agent{Name: "alex", Model: "claude-sonnet-4-6", System: "..."})

	for _, agent := range []string{"riley", "alex"} {
		body := mustJSON(t, CreateRoutineRequest{
			Title:        "Daily for " + agent,
			Instructions: "ping " + agent,
			Schedule:     Schedule{Frequency: FrequencyDaily, Time: "09:00", Timezone: "UTC"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent+"/schedules", bytes.NewReader(body))
		req.SetPathValue("agent", agent)
		w := httptest.NewRecorder()
		s.handleCreateAgentRoutine(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create for %s failed: %d %s", agent, w.Code, w.Body.String())
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/schedules", nil)
	listReq.SetPathValue("agent", "riley")
	listW := httptest.NewRecorder()
	s.handleListAgentRoutines(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d", listW.Code)
	}
	var got []AgentRoutineResponse
	_ = json.NewDecoder(listW.Body).Decode(&got)
	if len(got) != 1 {
		t.Fatalf("riley list len = %d, want 1; got = %+v", len(got), got)
	}
	if got[0].Agent != "riley" {
		t.Errorf("entry agent = %q, want riley", got[0].Agent)
	}
}

// TestUpdateAgentRoutine_PartialEdits covers PATCH semantics: only the
// supplied fields change; omitted fields stay put.
func TestUpdateAgentRoutine_PartialEdits(t *testing.T) {
	s := routineTestServer(t, "riley")
	body := mustJSON(t, CreateRoutineRequest{
		Title:        "Standup",
		Instructions: "post standup",
		Schedule:     Schedule{Frequency: FrequencyDaily, Time: "09:00", Timezone: "UTC"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/riley/schedules", bytes.NewReader(body))
	req.SetPathValue("agent", "riley")
	w := httptest.NewRecorder()
	s.handleCreateAgentRoutine(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %s", w.Body.String())
	}
	var created AgentRoutineResponse
	_ = json.NewDecoder(w.Body).Decode(&created)

	// Edit only the title — instructions and schedule must stay the same.
	newTitle := "Daily standup"
	patch := mustJSON(t, UpdateRoutineRequest{Title: &newTitle})
	pReq := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/riley/schedules/"+created.ID, bytes.NewReader(patch))
	pReq.SetPathValue("agent", "riley")
	pReq.SetPathValue("id", created.ID)
	pW := httptest.NewRecorder()
	s.handleUpdateAgentRoutine(pW, pReq)
	if pW.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", pW.Code, pW.Body.String())
	}
	var patched AgentRoutineResponse
	_ = json.NewDecoder(pW.Body).Decode(&patched)
	if patched.Title != "Daily standup" {
		t.Errorf("title = %q, want Daily standup", patched.Title)
	}
	if patched.Instructions != "post standup" {
		t.Errorf("instructions changed unexpectedly: %q", patched.Instructions)
	}
	if patched.Cron != created.Cron {
		t.Errorf("cron changed unexpectedly: %q → %q", created.Cron, patched.Cron)
	}

	// Edit schedule only — title and instructions stay.
	newSched := Schedule{Frequency: FrequencyDaily, Time: "10:30", Timezone: "UTC"}
	patch = mustJSON(t, UpdateRoutineRequest{Schedule: &newSched})
	pReq = httptest.NewRequest(http.MethodPatch, "/api/v1/agents/riley/schedules/"+created.ID, bytes.NewReader(patch))
	pReq.SetPathValue("agent", "riley")
	pReq.SetPathValue("id", created.ID)
	pW = httptest.NewRecorder()
	s.handleUpdateAgentRoutine(pW, pReq)
	if pW.Code != http.StatusOK {
		t.Fatalf("patch sched status = %d: %s", pW.Code, pW.Body.String())
	}
	_ = json.NewDecoder(pW.Body).Decode(&patched)
	if patched.Schedule.Time != "10:30" {
		t.Errorf("schedule.time = %q, want 10:30", patched.Schedule.Time)
	}
	if patched.Title != "Daily standup" {
		t.Errorf("title clobbered by schedule patch: %q", patched.Title)
	}
}

// TestDeleteAgentRoutine confirms the full cleanup path.
func TestDeleteAgentRoutine(t *testing.T) {
	s := routineTestServer(t, "riley")
	body := mustJSON(t, CreateRoutineRequest{
		Title:        "Standup",
		Instructions: "post standup",
		Schedule:     Schedule{Frequency: FrequencyDaily, Time: "09:00", Timezone: "UTC"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/riley/schedules", bytes.NewReader(body))
	req.SetPathValue("agent", "riley")
	w := httptest.NewRecorder()
	s.handleCreateAgentRoutine(w, req)
	var created AgentRoutineResponse
	_ = json.NewDecoder(w.Body).Decode(&created)

	dReq := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/riley/schedules/"+created.ID, nil)
	dReq.SetPathValue("agent", "riley")
	dReq.SetPathValue("id", created.ID)
	dW := httptest.NewRecorder()
	s.handleDeleteAgentRoutine(dW, dReq)
	if dW.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", dW.Code, dW.Body.String())
	}
	// GET should 404 now.
	gReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/schedules/"+created.ID, nil)
	gReq.SetPathValue("agent", "riley")
	gReq.SetPathValue("id", created.ID)
	gW := httptest.NewRecorder()
	s.handleGetAgentRoutine(gW, gReq)
	if gW.Code != http.StatusNotFound {
		t.Errorf("after delete GET = %d, want 404", gW.Code)
	}
}

// TestRoutineResponse_LegacyDSLJobsRendered checks that schedules created
// the old way (DSL tool → flat ScheduledJob with cron string) still
// surface in the new per-agent endpoint, with a synthesized `Custom`
// schedule echoing the cron.
func TestRoutineResponse_LegacyDSLJobsRendered(t *testing.T) {
	s := routineTestServer(t, "riley")
	// Insert directly into the store the way a DSL-created job lands —
	// no id, no title, no schedule_json.
	if err := s.store.UpsertScheduledJob(ScheduledJob{
		Name:      "legacy-job",
		Cron:      "0 9 * * *",
		AgentName: "riley",
		Message:   "ping",
		Enabled:   true,
	}); err != nil {
		t.Fatalf("UpsertScheduledJob: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/schedules", nil)
	listReq.SetPathValue("agent", "riley")
	listW := httptest.NewRecorder()
	s.handleListAgentRoutines(listW, listReq)
	var got []AgentRoutineResponse
	_ = json.NewDecoder(listW.Body).Decode(&got)
	if len(got) != 1 {
		t.Fatalf("list len = %d, want 1", len(got))
	}
	if got[0].ID != "legacy-job" {
		t.Errorf("legacy id should fall back to name: %q", got[0].ID)
	}
	if got[0].Title != "legacy-job" {
		t.Errorf("legacy title should fall back to name: %q", got[0].Title)
	}
	if got[0].Schedule.Frequency != FrequencyCustom {
		t.Errorf("legacy schedule.frequency = %q, want custom", got[0].Schedule.Frequency)
	}
	if got[0].Schedule.Cron != "0 9 * * *" {
		t.Errorf("legacy schedule.cron = %q, want raw cron", got[0].Schedule.Cron)
	}
}

// TestMarkScheduledJobRun_PersistsLastRunAt is a sanity test on the
// store method that the scheduler invokes after each fire.
func TestMarkScheduledJobRun_PersistsLastRunAt(t *testing.T) {
	store := newTestStore(t)
	_ = store.UpsertScheduledJob(ScheduledJob{
		Name: "job1", Cron: "* * * * *", AgentName: "riley", Message: "hi", Enabled: true,
	})
	stamp := time.Date(2026, 5, 11, 21, 0, 0, 0, time.UTC)
	if err := store.MarkScheduledJobRun("job1", stamp); err != nil {
		t.Fatalf("MarkScheduledJobRun: %v", err)
	}
	got, err := store.GetScheduledJobByID("job1")
	if err != nil {
		t.Fatalf("GetScheduledJobByID: %v", err)
	}
	if got == nil || got.LastRunAt == nil {
		t.Fatalf("last_run_at not set; got = %+v", got)
	}
	if !got.LastRunAt.Equal(stamp) {
		t.Errorf("last_run_at = %v, want %v", got.LastRunAt, stamp)
	}
}
