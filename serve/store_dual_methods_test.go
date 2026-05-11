package serve

import (
	"testing"
	"time"

	vega "github.com/everydev1618/govega"
)

// These tests exercise the ported Store methods against both SQLite and
// (when configured) Postgres. Each test asserts the same behavior on
// both backends — the goal of govega#61 phase 4.

func TestDualStore_EventsRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		now := time.Now().UTC().Truncate(time.Second)
		if err := store.InsertEvent(StoreEvent{Type: "process.started", AgentName: "riley", ProcessID: "p1", Timestamp: now}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
		if err := store.InsertEvent(StoreEvent{Type: "process.failed", AgentName: "alex", ProcessID: "p2", Timestamp: now.Add(time.Minute), Error: "rate limit hit"}); err != nil {
			t.Fatalf("InsertEvent 2: %v", err)
		}
		events, err := store.ListEvents(10)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2", len(events))
		}
		// Newest first.
		if events[0].Type != "process.failed" {
			t.Errorf("newest event type = %q, want process.failed", events[0].Type)
		}

		// Search by substring.
		hits, total, err := store.SearchEvents(ActivityFilter{Query: "rate limit"})
		if err != nil {
			t.Fatalf("SearchEvents: %v", err)
		}
		if total != 1 || len(hits) != 1 {
			t.Errorf("search total = %d, hits = %d, want 1 each", total, len(hits))
		}

		// Filter by agent.
		hits, total, _ = store.SearchEvents(ActivityFilter{Agent: "alex"})
		if total != 1 || hits[0].AgentName != "alex" {
			t.Errorf("agent filter wrong: total=%d hits=%+v", total, hits)
		}
	})
}

func TestDualStore_SettingsRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		if err := store.UpsertSetting(Setting{Key: "test.key", Value: "v1"}); err != nil {
			t.Fatalf("UpsertSetting: %v", err)
		}
		got, err := store.GetSetting("test.key")
		if err != nil {
			t.Fatalf("GetSetting: %v", err)
		}
		if got == nil || got.Value != "v1" {
			t.Errorf("got = %+v, want value v1", got)
		}

		// Upsert overwrites.
		_ = store.UpsertSetting(Setting{Key: "test.key", Value: "v2", Sensitive: true})
		got, _ = store.GetSetting("test.key")
		if got.Value != "v2" || !got.Sensitive {
			t.Errorf("after upsert: %+v", got)
		}

		// List.
		_ = store.UpsertSetting(Setting{Key: "other", Value: "x"})
		all, _ := store.ListSettings()
		if len(all) != 2 {
			t.Errorf("ListSettings len = %d, want 2", len(all))
		}

		// Missing key returns nil, nil.
		got, err = store.GetSetting("nope")
		if err != nil || got != nil {
			t.Errorf("missing: got=%v err=%v, want nil, nil", got, err)
		}

		// Delete.
		_ = store.DeleteSetting("test.key")
		got, _ = store.GetSetting("test.key")
		if got != nil {
			t.Errorf("after delete got = %+v, want nil", got)
		}
	})
}

func TestDualStore_ComposedAgentsRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		temp := 0.7
		now := time.Now().UTC().Truncate(time.Second)
		err := store.InsertComposedAgent(ComposedAgent{
			Name:           "riley",
			DisplayName:    "Riley",
			Title:          "CPO",
			Description:    "Chief product officer",
			Avatar:         "f11",
			Icon:           "Sparkles",
			AvatarGradient: []string{"#A78BFA", "#7C3AED"},
			Model:          "claude-sonnet-4-6",
			Tools:          []string{"read_file", "write_file"},
			Team:           []string{"alex"},
			Skills:         []string{"writing"},
			System:         "You are Riley.",
			Temperature:    &temp,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if err != nil {
			t.Fatalf("InsertComposedAgent: %v", err)
		}
		all, err := store.ListComposedAgents()
		if err != nil {
			t.Fatalf("ListComposedAgents: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("len = %d, want 1", len(all))
		}
		got := all[0]
		if got.DisplayName != "Riley" || got.Title != "CPO" || got.Icon != "Sparkles" {
			t.Errorf("identity fields wrong: %+v", got)
		}
		if len(got.AvatarGradient) != 2 || got.AvatarGradient[0] != "#A78BFA" {
			t.Errorf("avatar_gradient = %v", got.AvatarGradient)
		}
		if got.Temperature == nil || *got.Temperature != 0.7 {
			t.Errorf("temperature = %v, want 0.7", got.Temperature)
		}
		if len(got.Tools) != 2 || len(got.Team) != 1 {
			t.Errorf("tools/team round-trip lost: %+v / %+v", got.Tools, got.Team)
		}

		// Upsert updates without duplicating.
		_ = store.InsertComposedAgent(ComposedAgent{Name: "riley", DisplayName: "Riley2", Model: "claude-opus-4-7", CreatedAt: now})
		all, _ = store.ListComposedAgents()
		if len(all) != 1 || all[0].DisplayName != "Riley2" {
			t.Errorf("upsert wrong: %+v", all)
		}

		// Delete.
		if err := store.DeleteComposedAgent("riley"); err != nil {
			t.Errorf("DeleteComposedAgent: %v", err)
		}
		all, _ = store.ListComposedAgents()
		if len(all) != 0 {
			t.Errorf("after delete len = %d", len(all))
		}
	})
}

func TestDualStore_ChatMessages(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		if err := store.InsertChatMessage("riley", "user", "hi", nil); err != nil {
			t.Fatalf("InsertChatMessage: %v", err)
		}
		activities := []vega.ToolActivity{{ToolName: "read_file", DurationMs: 12}}
		if err := store.InsertChatMessage("riley", "assistant", "ok", activities); err != nil {
			t.Fatalf("InsertChatMessage 2: %v", err)
		}
		msgs, err := store.ListChatMessages("riley")
		if err != nil {
			t.Fatalf("ListChatMessages: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("len = %d, want 2", len(msgs))
		}
		if msgs[1].Role != "assistant" || len(msgs[1].ToolActivities) != 1 {
			t.Errorf("assistant message: %+v", msgs[1])
		}
		_ = store.DeleteChatMessages("riley")
		msgs, _ = store.ListChatMessages("riley")
		if len(msgs) != 0 {
			t.Errorf("after delete len = %d", len(msgs))
		}
	})
}

func TestDualStore_ScheduledJobsRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		now := time.Now().UTC().Truncate(time.Second)
		err := store.UpsertScheduledJob(ScheduledJob{
			ID: "sched_1", Name: "sched_1", Title: "Daily standup",
			Cron: "0 9 * * *", AgentName: "riley", Message: "post standup",
			ScheduleJSON: `{"frequency":"daily"}`, Enabled: true,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertScheduledJob: %v", err)
		}
		got, err := store.GetScheduledJobByID("sched_1")
		if err != nil || got == nil {
			t.Fatalf("GetScheduledJobByID: got=%v err=%v", got, err)
		}
		if got.Title != "Daily standup" || got.AgentName != "riley" {
			t.Errorf("got = %+v", got)
		}

		// Mark run stamps last_run_at.
		stamp := now.Add(1 * time.Hour)
		if err := store.MarkScheduledJobRun("sched_1", stamp); err != nil {
			t.Fatalf("MarkScheduledJobRun: %v", err)
		}
		got, _ = store.GetScheduledJobByID("sched_1")
		if got.LastRunAt == nil || !got.LastRunAt.Equal(stamp) {
			t.Errorf("last_run_at = %v, want %v", got.LastRunAt, stamp)
		}

		// List + delete.
		jobs, _ := store.ListScheduledJobs()
		if len(jobs) != 1 {
			t.Errorf("list len = %d, want 1", len(jobs))
		}
		_ = store.DeleteScheduledJob("sched_1")
		got, _ = store.GetScheduledJobByID("sched_1")
		if got != nil {
			t.Errorf("after delete got = %+v", got)
		}
	})
}

func TestDualStore_BrainFiles(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		content := []byte("# guide\n\nhello.\n")
		err := store.InsertAgentBrainFile(AgentBrainFile{
			ID: "brain_a", AgentName: "riley", Name: "guide.md",
			MimeType: "text/markdown", SizeBytes: int64(len(content)),
			Content: content,
		})
		if err != nil {
			t.Fatalf("InsertAgentBrainFile: %v", err)
		}
		list, err := store.ListAgentBrainFiles("riley")
		if err != nil || len(list) != 1 {
			t.Fatalf("list: len=%d err=%v", len(list), err)
		}
		if list[0].SizeBytes != int64(len(content)) {
			t.Errorf("size_bytes = %d, want %d", list[0].SizeBytes, len(content))
		}

		got, err := store.GetAgentBrainFile("riley", "brain_a")
		if err != nil || got == nil {
			t.Fatalf("Get: got=%v err=%v", got, err)
		}
		if string(got.Content) != string(content) {
			t.Errorf("content round-trip lost: %q vs %q", got.Content, content)
		}

		// Per-agent scoping.
		other, _ := store.GetAgentBrainFile("alex", "brain_a")
		if other != nil {
			t.Errorf("cross-agent read returned %+v", other)
		}

		// Delete.
		if err := store.DeleteAgentBrainFile("riley", "brain_a"); err != nil {
			t.Errorf("DeleteAgentBrainFile: %v", err)
		}
		got, _ = store.GetAgentBrainFile("riley", "brain_a")
		if got != nil {
			t.Errorf("after delete got = %+v", got)
		}
	})
}

func TestDualStore_UserMemory(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		if err := store.UpsertUserMemory("u1", "riley", "profile", "etienne — eng lead"); err != nil {
			t.Fatalf("UpsertUserMemory: %v", err)
		}
		if err := store.UpsertUserMemory("u1", "riley", "topics", "kanban, scheduling"); err != nil {
			t.Fatalf("UpsertUserMemory 2: %v", err)
		}
		mems, err := store.GetUserMemory("u1", "riley")
		if err != nil {
			t.Fatalf("GetUserMemory: %v", err)
		}
		if len(mems) != 2 {
			t.Fatalf("len = %d, want 2", len(mems))
		}
		// Upsert replaces.
		_ = store.UpsertUserMemory("u1", "riley", "profile", "etienne — cto")
		mems, _ = store.GetUserMemory("u1", "riley")
		seen := map[string]string{}
		for _, m := range mems {
			seen[m.Layer] = m.Content
		}
		if seen["profile"] != "etienne — cto" {
			t.Errorf("profile = %q after upsert", seen["profile"])
		}
		// Delete all for user/agent.
		_ = store.DeleteUserMemory("u1", "riley")
		mems, _ = store.GetUserMemory("u1", "riley")
		if len(mems) != 0 {
			t.Errorf("after delete len = %d", len(mems))
		}
	})
}

func TestDualStore_AgentSpendRollup(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		now := time.Now().UTC().Truncate(time.Second)
		insert := func(processID string, started time.Time, cost float64) {
			t.Helper()
			if err := store.InsertProcessSnapshot(ProcessSnapshot{
				ProcessID: processID, AgentName: "riley", Status: "completed",
				CostUSD: cost, StartedAt: started,
			}); err != nil {
				t.Fatalf("InsertProcessSnapshot: %v", err)
			}
		}
		insert("p1", now, 1.0)
		insert("p1", now, 3.0) // later snapshot
		insert("p2", now, 2.5)

		total, err := store.AgentSpendInPeriod("riley", time.Time{}, time.Time{})
		if err != nil {
			t.Fatalf("AgentSpendInPeriod: %v", err)
		}
		want := 3.0 + 2.5
		if total != want {
			t.Errorf("total = %v, want %v", total, want)
		}
	})
}

func TestDualStore_WorkspaceFiles(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		err := store.InsertWorkspaceFile(WorkspaceFile{
			Path: "report.md", Agent: "riley", ProcessID: "p1", Operation: "write",
		})
		if err != nil {
			t.Fatalf("InsertWorkspaceFile: %v", err)
		}
		_ = store.InsertWorkspaceFile(WorkspaceFile{Path: "notes.md", Agent: "alex"})

		all, _ := store.ListWorkspaceFiles("")
		if len(all) != 2 {
			t.Errorf("all len = %d, want 2", len(all))
		}
		rileyOnly, _ := store.ListWorkspaceFiles("riley")
		if len(rileyOnly) != 1 || rileyOnly[0].Path != "report.md" {
			t.Errorf("riley list = %+v", rileyOnly)
		}
		agents, _ := store.ListWorkspaceFileAgents()
		if len(agents) != 2 {
			t.Errorf("agents = %v", agents)
		}
	})
}
