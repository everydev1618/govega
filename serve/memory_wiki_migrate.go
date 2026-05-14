package serve

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// --- Legacy formatters (migrated from memory_extract.go) ---
//
// Render the old typed user_memory JSON blobs back to readable
// markdown so the migration can drop them into wiki pages. The
// original extractor is gone (refs govega#71), but the formatters
// stay until the legacy tables are dropped and migration retires.

// journalEntry is the legacy coaching-session row stored as JSON in
// user_memory.journal.
type journalEntry struct {
	Date           string   `json:"date"`
	Challenge      string   `json:"challenge"`
	Advice         string   `json:"advice"`
	ActionItems    []string `json:"action_items"`
	FrameworksUsed []string `json:"frameworks_used"`
}

func formatProfileContent(content string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return content
	}
	var b strings.Builder
	for k, v := range data {
		b.WriteString(fmt.Sprintf("%s: %v\n", k, v))
	}
	return b.String()
}

func formatTopicsContent(content string) string {
	var topics map[string]string
	if err := json.Unmarshal([]byte(content), &topics); err != nil {
		return content
	}
	var b strings.Builder
	for topic, summary := range topics {
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", topic, summary))
	}
	return b.String()
}

func formatNotesContent(content string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return content
	}
	var b strings.Builder
	for k, v := range data {
		b.WriteString(fmt.Sprintf("%s: %v\n", k, v))
	}
	return b.String()
}

func formatJournalContent(content string) string {
	var entries []journalEntry
	if err := json.Unmarshal([]byte(content), &entries); err != nil {
		return content
	}
	start := 0
	if len(entries) > 10 {
		start = len(entries) - 10
	}
	var b strings.Builder
	for _, e := range entries[start:] {
		b.WriteString(fmt.Sprintf("- %s: %s", e.Date, e.Challenge))
		if e.Advice != "" {
			b.WriteString(fmt.Sprintf(" — %s", e.Advice))
		}
		if len(e.ActionItems) > 0 {
			b.WriteString(fmt.Sprintf(" Action items: %s.", strings.Join(e.ActionItems, ", ")))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Refs govega#71. One-shot migration from the typed user_memory and
// memory_items tables into wiki pages. Runs at boot (Server.Start),
// idempotent via the settings flag below.

const wikiMigrationSettingKey = "memory_wiki_migrated_v1"

// MigrationReport summarizes what a migration run did. PagesWritten
// counts shared + agent pages combined. Exactly one of JustApplied /
// AlreadyApplied is true.
type MigrationReport struct {
	JustApplied    bool
	AlreadyApplied bool
	PagesWritten   int
	Users          int
	AgentsTouched  int
}

// migrateToWikiMemory walks user_memory + memory_items, builds wiki
// pages, and stamps the settings flag. Re-running is a no-op once
// the flag is set. Hand-edits made between runs are preserved
// because we never overwrite once the flag is in place.
func migrateToWikiMemory(store Store) (MigrationReport, error) {
	var report MigrationReport

	flag, err := store.GetSetting(wikiMigrationSettingKey)
	if err != nil {
		return report, fmt.Errorf("read migration flag: %w", err)
	}
	if flag != nil && flag.Value == "1" {
		report.AlreadyApplied = true
		return report, nil
	}

	userMems, err := store.ListAllUserMemory()
	if err != nil {
		return report, fmt.Errorf("list user_memory: %w", err)
	}
	items, err := store.ListAllMemoryItems()
	if err != nil {
		return report, fmt.Errorf("list memory_items: %w", err)
	}

	// Group user_memory by (userID, agent, layer) — already sorted by
	// the bulk-load query — and memory_items by (userID, agent).
	userByKey := map[userKey]UserMemory{}
	userIDs := map[string]struct{}{}
	agentsByUser := map[string]map[string]struct{}{}
	for _, m := range userMems {
		userByKey[userKey{m.UserID, m.Agent, m.Layer}] = m
		userIDs[m.UserID] = struct{}{}
		if _, ok := agentsByUser[m.UserID]; !ok {
			agentsByUser[m.UserID] = map[string]struct{}{}
		}
		agentsByUser[m.UserID][m.Agent] = struct{}{}
	}
	type agentKey struct{ userID, agent string }
	itemsByAgent := map[agentKey][]MemoryItem{}
	for _, it := range items {
		k := agentKey{it.UserID, it.Agent}
		itemsByAgent[k] = append(itemsByAgent[k], it)
		userIDs[it.UserID] = struct{}{}
		if _, ok := agentsByUser[it.UserID]; !ok {
			agentsByUser[it.UserID] = map[string]struct{}{}
		}
		agentsByUser[it.UserID][it.Agent] = struct{}{}
	}

	for uid := range userIDs {
		// agents talking to this user, sorted for deterministic output
		agents := make([]string, 0, len(agentsByUser[uid]))
		for a := range agentsByUser[uid] {
			agents = append(agents, a)
		}
		sort.Strings(agents)

		// ---- Shared user wiki: profile / topics / notes ----
		var indexEntries []string
		for _, layer := range []string{"profile", "topics", "notes"} {
			content := concatLayerAcrossAgents(userByKey, uid, agents, layer)
			if content == "" {
				continue
			}
			page := layer + ".md"
			frontmatter := fmt.Sprintf("source: migrated from user_memory layer %q (govega#71)", layer)
			if err := store.UpsertMemoryPage(MemoryPage{
				Scope: MemoryScopeUser, ScopeID: uid, UserID: uid,
				Path: page, Content: content, Frontmatter: frontmatter,
			}); err != nil {
				return report, fmt.Errorf("write shared %s for %s: %w", page, uid, err)
			}
			report.PagesWritten++
			indexEntries = append(indexEntries, "- ["+strings.TrimSuffix(page, ".md")+"]("+page+")")
		}

		// ---- Per-agent wiki: journal + legacy-items ----
		for _, ag := range agents {
			if jm, ok := userByKey[userKey{uid, ag, "journal"}]; ok && strings.TrimSpace(jm.Content) != "" {
				body := "## Journal (migrated from user_memory.journal — govega#71)\n\n" + formatJournalContent(jm.Content)
				if err := store.UpsertMemoryPage(MemoryPage{
					Scope: MemoryScopeAgent, ScopeID: ag, UserID: uid,
					Path: "legacy-journal.md", Content: body,
					Frontmatter: "source: migrated from user_memory.journal (govega#71)",
				}); err != nil {
					return report, fmt.Errorf("write journal %s/%s: %w", uid, ag, err)
				}
				report.PagesWritten++
				report.AgentsTouched++
			}
			if its := itemsByAgent[agentKey{uid, ag}]; len(its) > 0 {
				body := renderLegacyItems(its)
				if err := store.UpsertMemoryPage(MemoryPage{
					Scope: MemoryScopeAgent, ScopeID: ag, UserID: uid,
					Path: "legacy-items.md", Content: body,
					Frontmatter: "source: migrated from memory_items (govega#71)",
				}); err != nil {
					return report, fmt.Errorf("write items %s/%s: %w", uid, ag, err)
				}
				report.PagesWritten++
				// AgentsTouched counts agents we wrote *something* private
				// for. If we already counted via journal, don't double up.
				if _, hadJournal := userByKey[userKey{uid, ag, "journal"}]; !hadJournal {
					report.AgentsTouched++
				}
			}
		}

		// ---- Shared MEMORY.md ----
		if len(indexEntries) > 0 {
			indexBody := "# Memory index\n\nMigrated from the typed user_memory tables (govega#71). The curator agent will reorganize these into proper topic pages over time.\n\n" + strings.Join(indexEntries, "\n") + "\n"
			if err := store.UpsertMemoryPage(MemoryPage{
				Scope: MemoryScopeUser, ScopeID: uid, UserID: uid,
				Path: "MEMORY.md", Content: indexBody,
				Frontmatter: "name: index\nsource: wiki migration v1",
			}); err != nil {
				return report, fmt.Errorf("write index for %s: %w", uid, err)
			}
			report.PagesWritten++
			if err := store.ReplaceMemoryLinks(
				MemoryScopeUser, uid, uid, "MEMORY.md",
				extractMemoryLinks(indexBody),
			); err != nil {
				return report, fmt.Errorf("link graph for index %s: %w", uid, err)
			}
		}
		report.Users++
	}

	if err := store.UpsertSetting(Setting{Key: wikiMigrationSettingKey, Value: "1"}); err != nil {
		return report, fmt.Errorf("set migration flag: %w", err)
	}
	report.JustApplied = true
	return report, nil
}

// userKey is the (userID, agent, layer) tuple that user_memory rows
// are grouped by during migration.
type userKey struct{ userID, agent, layer string }

// concatLayerAcrossAgents pulls the same layer from every agent for
// userID and stitches them together with per-agent headers. Returns
// "" when no agent has data for the layer. The original JSON content
// is rendered through the legacy formatters so the wiki page reads
// as markdown rather than raw JSON blobs.
func concatLayerAcrossAgents(byKey map[userKey]UserMemory, userID string, agents []string, layer string) string {
	type entry struct {
		agent   string
		content string
	}
	var entries []entry
	for _, ag := range agents {
		m, ok := byKey[userKey{userID, ag, layer}]
		if !ok {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		entries = append(entries, entry{agent: ag, content: renderLegacyLayer(layer, m.Content)})
	}
	if len(entries) == 0 {
		return ""
	}
	// Single agent: render directly with no per-agent header.
	if len(entries) == 1 {
		return entries[0].content + "\n"
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("## From %s\n\n", e.agent))
		b.WriteString(e.content)
		if !strings.HasSuffix(e.content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderLegacyLayer formats a single layer's stored JSON as readable
// markdown. Falls back to the raw content when JSON parsing fails so
// we never lose data during migration.
func renderLegacyLayer(layer, content string) string {
	switch layer {
	case "profile":
		return formatProfileContent(content)
	case "topics":
		return formatTopicsContent(content)
	case "notes":
		return formatNotesContent(content)
	}
	return content
}

// renderLegacyItems flattens grouped MemoryItems into a single page,
// sorted by topic then created_at. One header per topic; items inside
// rendered as bullets with their type and tags. Curator will refile
// these into proper topic pages later.
func renderLegacyItems(items []MemoryItem) string {
	if len(items) == 0 {
		return ""
	}
	byTopic := map[string][]MemoryItem{}
	for _, it := range items {
		topic := strings.TrimSpace(it.Topic)
		if topic == "" {
			topic = "(no topic)"
		}
		byTopic[topic] = append(byTopic[topic], it)
	}
	topics := make([]string, 0, len(byTopic))
	for t := range byTopic {
		topics = append(topics, t)
	}
	sort.Strings(topics)

	var b strings.Builder
	b.WriteString("# Legacy memory items\n\nMigrated from `memory_items` (govega#71). The curator agent will reorganize these into proper topic pages over time.\n\n")
	for _, t := range topics {
		b.WriteString("## " + t + "\n\n")
		for _, it := range byTopic[t] {
			b.WriteString(fmt.Sprintf("- [%s] %s", it.Type, it.Content))
			if strings.TrimSpace(it.Tags) != "" {
				b.WriteString(" — tags: " + it.Tags)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
