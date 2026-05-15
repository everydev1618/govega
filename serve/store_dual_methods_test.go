package serve

import (
	"database/sql"
	"strings"
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

func TestDualStore_MemoryItemsDedup(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		id1, err := store.InsertMemoryItem(MemoryItem{
			UserID: "u1", Agent: "riley", Type: MemoryTypeReference,
			Topic: "weather", Content: "sunny in NYC", Tags: "weather",
		})
		if err != nil {
			t.Fatalf("InsertMemoryItem: %v", err)
		}
		// Same content → dedups to same id, merges tags.
		id2, err := store.InsertMemoryItem(MemoryItem{
			UserID: "u1", Agent: "riley", Type: MemoryTypeReference,
			Topic: "weather", Content: "sunny in NYC", Tags: "city",
		})
		if err != nil {
			t.Fatalf("InsertMemoryItem dedup: %v", err)
		}
		if id1 != id2 {
			t.Errorf("dedup failed: id1=%d id2=%d", id1, id2)
		}

		hits, err := store.SearchMemoryItems("u1", "riley", "sunny", 10)
		if err != nil {
			t.Fatalf("SearchMemoryItems: %v", err)
		}
		if len(hits) != 1 {
			t.Errorf("search len = %d, want 1", len(hits))
		}
		// Merged tags include both "weather" and "city".
		if !strings.Contains(hits[0].Tags, "weather") || !strings.Contains(hits[0].Tags, "city") {
			t.Errorf("tags not merged: %q", hits[0].Tags)
		}

		// Topic listing.
		byTopic, _ := store.ListMemoryItemsByTopic("u1", "riley", "weather")
		if len(byTopic) != 1 {
			t.Errorf("topic len = %d", len(byTopic))
		}

		// Delete.
		if err := store.DeleteMemoryItem(id1); err != nil {
			t.Errorf("DeleteMemoryItem: %v", err)
		}
	})
}

func TestDualStore_ChannelsRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		if err := store.CreateChannel("ch_1", "general", "general chat", "iris", []string{"riley", "alex"}, ""); err != nil {
			t.Fatalf("CreateChannel: %v", err)
		}
		ch, err := store.GetChannel("general")
		if err != nil || ch == nil {
			t.Fatalf("GetChannel: %v %v", ch, err)
		}
		if ch.ID != "ch_1" || len(ch.Team) != 2 {
			t.Errorf("got = %+v", ch)
		}

		all, _ := store.ListAllChannels()
		if len(all) != 1 {
			t.Errorf("ListAllChannels len = %d", len(all))
		}
		mine, _ := store.ListChannelsForAgent("riley")
		if len(mine) != 1 {
			t.Errorf("ListChannelsForAgent riley len = %d", len(mine))
		}

		// Rename.
		newName := "team"
		if err := store.UpdateChannelMeta("general", &newName, nil); err != nil {
			t.Errorf("UpdateChannelMeta rename: %v", err)
		}
		ch, _ = store.GetChannel("team")
		if ch == nil {
			t.Error("renamed channel not found")
		}

		// Team update.
		if err := store.UpdateChannelTeam("team", []string{"riley", "alex", "sofia"}); err != nil {
			t.Errorf("UpdateChannelTeam: %v", err)
		}

		// FindChannelForAgents.
		id, name, _ := store.FindChannelForAgents("riley", "alex")
		if id != "ch_1" || name != "team" {
			t.Errorf("FindChannelForAgents = %q,%q", id, name)
		}

		// Delete.
		_ = store.DeleteChannel("team")
		ch, _ = store.GetChannel("team")
		if ch != nil {
			t.Errorf("after delete: %+v", ch)
		}
	})
}

func TestDualStore_ChannelMessagesAndReadCursor(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		_ = store.CreateChannel("ch_x", "x", "", "iris", []string{"riley"}, "")
		id1, err := store.InsertChannelMessage("ch_x", "riley", "assistant", "hi", nil, "{}", "", nil)
		if err != nil {
			t.Fatalf("InsertChannelMessage: %v", err)
		}
		// Thread reply.
		_, err = store.InsertChannelMessage("ch_x", "alex", "assistant", "yo", &id1, "{}", "", nil)
		if err != nil {
			t.Fatalf("InsertChannelMessage reply: %v", err)
		}

		// Top-level list shows the parent + reply_count.
		top, err := store.ListChannelMessages("ch_x", 100)
		if err != nil {
			t.Fatalf("ListChannelMessages: %v", err)
		}
		if len(top) != 1 || top[0].ReplyCount != 1 {
			t.Errorf("top = %+v", top)
		}
		if len(top[0].ReplySenders) == 0 {
			t.Errorf("reply_senders empty: %+v", top[0])
		}

		// Thread list.
		thread, _ := store.ListThreadMessages("ch_x", id1)
		if len(thread) != 2 {
			t.Errorf("thread len = %d", len(thread))
		}

		// Mark read clears unread.
		// (Add a chat message first so ChatUnreadCounts has something to count.)
		_ = store.InsertChatMessage("riley", "user", "hello", nil)
		_ = store.InsertChatMessage("riley", "assistant", "hi", nil)
		counts, err := store.ChatUnreadCounts("u1")
		if err != nil {
			t.Fatalf("ChatUnreadCounts: %v", err)
		}
		if counts["riley"] != 1 {
			t.Errorf("unread riley = %d, want 1", counts["riley"])
		}
		_ = store.MarkChatRead("riley", "u1")
		counts, _ = store.ChatUnreadCounts("u1")
		if counts["riley"] != 0 {
			t.Errorf("after mark read unread = %d, want 0", counts["riley"])
		}

		// MarkChannelRead doesn't error.
		if err := store.MarkChannelRead("ch_x", "u1"); err != nil {
			t.Errorf("MarkChannelRead: %v", err)
		}

		// Recent + lightweight list.
		recent, _ := store.RecentChannelMessages("ch_x", 10)
		if len(recent) == 0 {
			t.Errorf("RecentChannelMessages empty")
		}
	})
}

func TestDualStore_Inbox(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		id, err := store.InsertInboxItem("riley", "blocked on X", "need login creds", "high")
		if err != nil {
			t.Fatalf("InsertInboxItem: %v", err)
		}
		items, _ := store.ListInboxItems("pending", 10)
		if len(items) != 1 || items[0].ID != id {
			t.Errorf("list = %+v", items)
		}
		one, _ := store.GetInboxItem(id)
		if one == nil || one.Subject != "blocked on X" {
			t.Errorf("get = %+v", one)
		}
		_ = store.ResolveInboxItem(id, "creds shared")
		one, _ = store.GetInboxItem(id)
		if one.Status != "resolved" || one.Resolution != "creds shared" {
			t.Errorf("after resolve = %+v", one)
		}
		n, _ := store.DeleteResolvedInboxItems()
		if n != 1 {
			t.Errorf("DeleteResolvedInboxItems n = %d, want 1", n)
		}
	})
}

// TestDualStore_DeleteInboxItem covers the human-driven dismiss path:
// the user wants a single inbox item gone, regardless of status. The
// row-not-found case must surface as sql.ErrNoRows so HTTP handlers
// can map it to a 404 instead of silently 200ing.
func TestDualStore_DeleteInboxItem(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		// Pending item is deletable.
		id, err := store.InsertInboxItem("riley", "noise from heartbeat", "ignore", "normal")
		if err != nil {
			t.Fatalf("InsertInboxItem: %v", err)
		}
		if err := store.DeleteInboxItem(id); err != nil {
			t.Fatalf("DeleteInboxItem(pending): %v", err)
		}
		if got, _ := store.GetInboxItem(id); got != nil {
			t.Errorf("after delete, expected nil, got %+v", got)
		}

		// Resolved item is also deletable (no status gate).
		id2, _ := store.InsertInboxItem("riley", "already handled", "body", "normal")
		_ = store.ResolveInboxItem(id2, "done")
		if err := store.DeleteInboxItem(id2); err != nil {
			t.Errorf("DeleteInboxItem(resolved): %v", err)
		}

		// Non-existent id reports sql.ErrNoRows.
		if err := store.DeleteInboxItem(99999); err != sql.ErrNoRows {
			t.Errorf("DeleteInboxItem(missing): got %v, want sql.ErrNoRows", err)
		}
	})
}

func TestDualStore_PromptHistory(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		id, err := store.InsertPromptHistory("schedule a standup")
		if err != nil {
			t.Fatalf("InsertPromptHistory: %v", err)
		}
		_, _ = store.InsertPromptHistory("draft the policy")

		all, _ := store.ListPromptHistory(10)
		if len(all) != 2 {
			t.Errorf("list len = %d", len(all))
		}
		hits, _ := store.SearchPromptHistory("standup", 10)
		if len(hits) != 1 {
			t.Errorf("search standup len = %d", len(hits))
		}
		if err := store.DeletePromptHistory(id); err != nil {
			t.Errorf("DeletePromptHistory: %v", err)
		}
	})
}

func TestDualStore_TasksRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		// Insert.
		if err := store.InsertTask(Task{
			ID: "t1", Title: "Ship the kanban", Status: TaskStatusTodo, Assignee: "riley",
		}); err != nil {
			t.Fatalf("InsertTask: %v", err)
		}
		// Get.
		got, _ := store.GetTask("t1")
		if got == nil || got.Title != "Ship the kanban" {
			t.Fatalf("GetTask: %+v", got)
		}
		// Update.
		newDesc := "Move to /tasks"
		if err := store.UpdateTask("t1", TaskUpdate{Description: &newDesc}); err != nil {
			t.Errorf("UpdateTask: %v", err)
		}
		got, _ = store.GetTask("t1")
		if got.Description != newDesc {
			t.Errorf("description = %q", got.Description)
		}
		// List with filter.
		list, _ := store.ListTasks(TaskFilter{Status: []string{TaskStatusTodo}})
		if len(list) != 1 {
			t.Errorf("list todos = %d, want 1", len(list))
		}
		// Comment.
		commentID, err := store.AddTaskComment("t1", "iris", "first pass")
		if err != nil {
			t.Errorf("AddTaskComment: %v", err)
		}
		comments, _ := store.ListTaskComments("t1")
		if len(comments) != 1 || comments[0].ID != commentID {
			t.Errorf("comments = %+v", comments)
		}
		// Process link (idempotent).
		_ = store.LinkTaskProcess("t1", "proc-A")
		_ = store.LinkTaskProcess("t1", "proc-A")
		procs, _ := store.ListTaskProcesses("t1")
		if len(procs) != 1 {
			t.Errorf("LinkTaskProcess not idempotent: %+v", procs)
		}
		// Claim.
		_ = store.ClaimTask("t1", "alex")
		got, _ = store.GetTask("t1")
		if got.Status != TaskStatusDoing || got.Assignee != "alex" {
			t.Errorf("after claim: %+v", got)
		}
		// Stats.
		stats, _ := store.TaskStatsByAssignee()
		if stats["alex"].AssignedTasks != 1 {
			t.Errorf("stats alex = %+v", stats["alex"])
		}
		// Delete.
		if err := store.DeleteTask("t1"); err != nil {
			t.Errorf("DeleteTask: %v", err)
		}
		got, _ = store.GetTask("t1")
		if got != nil {
			t.Errorf("after delete: %+v", got)
		}
	})
}

func TestDualStore_ResetData(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		_ = store.InsertEvent(StoreEvent{Type: "test", Timestamp: time.Now().UTC()})
		_ = store.InsertTask(Task{ID: "t1", Title: "x", Status: TaskStatusTodo})
		_ = store.UpsertSetting(Setting{Key: "preserved", Value: "yes"})
		_ = store.UpsertMemoryPage(MemoryPage{
			Scope: MemoryScopeUser, ScopeID: "u1", UserID: "u1",
			Path: "MEMORY.md", Content: "- [a](a.md)",
		})
		_ = store.UpsertMemoryPage(MemoryPage{
			Scope: MemoryScopeUser, ScopeID: "u1", UserID: "u1",
			Path: "a.md", Content: "page a",
		})
		_ = store.ReplaceMemoryLinks(MemoryScopeUser, "u1", "u1", "MEMORY.md", []string{"a.md"})

		if err := store.ResetData(); err != nil {
			t.Fatalf("ResetData: %v", err)
		}
		// Events + tasks cleared.
		events, _ := store.ListEvents(10)
		if len(events) != 0 {
			t.Errorf("events after reset = %d", len(events))
		}
		tasks, _ := store.ListTasks(TaskFilter{})
		if len(tasks) != 0 {
			t.Errorf("tasks after reset = %d", len(tasks))
		}
		// Wiki memory pages + links cleared.
		pages, _ := store.ListMemoryPages(MemoryScopeUser, "u1", "u1", "")
		if len(pages) != 0 {
			t.Errorf("memory pages after reset = %d, want 0", len(pages))
		}
		links, _ := store.ListMemoryLinks(MemoryScopeUser, "u1", "u1")
		if len(links) != 0 {
			t.Errorf("memory links after reset = %d, want 0", len(links))
		}
		// Settings preserved.
		got, _ := store.GetSetting("preserved")
		if got == nil || got.Value != "yes" {
			t.Errorf("setting clobbered by reset: %+v", got)
		}
	})
}

func TestDualStore_AgentBudgetRoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store Store) {
		// Missing rows return nil, nil — no error, no row.
		got, err := store.GetAgentBudget("riley")
		if err != nil {
			t.Fatalf("GetAgentBudget missing: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for missing budget, got %+v", got)
		}

		// Upsert sets the cap.
		cap := 25.0
		err = store.UpsertAgentBudget(AgentBudget{
			AgentName: "riley", BudgetCap: &cap,
			SoftAlertThreshold: 0.75, Enabled: true,
		})
		if err != nil {
			t.Fatalf("UpsertAgentBudget: %v", err)
		}
		got, _ = store.GetAgentBudget("riley")
		if got == nil || got.BudgetCap == nil || *got.BudgetCap != 25.0 {
			t.Errorf("after set: %+v", got)
		}
		if got.SoftAlertThreshold != 0.75 || !got.Enabled {
			t.Errorf("threshold/enabled wrong: %+v", got)
		}

		// Upsert replaces — clear the cap.
		err = store.UpsertAgentBudget(AgentBudget{
			AgentName: "riley", BudgetCap: nil,
			SoftAlertThreshold: 0.8, Enabled: false,
		})
		if err != nil {
			t.Fatalf("UpsertAgentBudget clear: %v", err)
		}
		got, _ = store.GetAgentBudget("riley")
		if got == nil || got.BudgetCap != nil {
			t.Errorf("after clear: %+v", got)
		}
		if got.Enabled {
			t.Errorf("enabled should be false")
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
