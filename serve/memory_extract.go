package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/everydev1618/govega/llm"
)

// extractionResult is the JSON structure returned by the extraction LLM.
type extractionResult struct {
	ProfileUpdates map[string]any `json:"profile_updates"`
	TopicUpdates   []topicUpdate  `json:"topic_updates"`
	NotesUpdates   map[string]any `json:"notes_updates"`
}

// topicUpdate is a project/topic summary extracted from conversation.
type topicUpdate struct {
	Topic   string   `json:"topic"`
	Summary string   `json:"summary"`
	Details []string `json:"details"`
	Tags    []string `json:"tags"`
}

// extractMemory runs an async LLM call to extract memory from the latest exchange.
func (s *Server) extractMemory(userID, agent, userMsg, response string) {
	// Only one extraction at a time; skip if another is in progress.
	select {
	case s.extractSem <- struct{}{}:
		defer func() { <-s.extractSem }()
	default:
		slog.Debug("memory extraction skipped: another extraction in progress")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	extractLLM := s.getExtractLLM()
	if extractLLM == nil {
		slog.Warn("memory extraction skipped: no extract LLM available")
		return
	}

	// Load existing memory for context.
	memories, err := s.store.GetUserMemory(userID, agent)
	if err != nil {
		slog.Error("memory extraction: failed to load existing memory", "error", err)
		return
	}

	existingJSON := buildExistingMemoryJSON(memories)

	prompt := buildExtractionPrompt(existingJSON, userMsg, response)

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}

	resp, err := extractLLM.Generate(ctx, messages, nil)
	if err != nil {
		slog.Error("memory extraction: LLM call failed", "error", err)
		return
	}

	// Parse the JSON from the response.
	result, err := parseExtractionResult(resp.Content)
	if err != nil {
		slog.Error("memory extraction: failed to parse result", "error", err, "content", resp.Content)
		return
	}

	// Upsert profile if we got updates.
	if result.ProfileUpdates != nil {
		existing := findLayer(memories, "profile")
		merged := mergeProfile(existing, result.ProfileUpdates)
		content, _ := json.Marshal(merged)
		if err := s.store.UpsertUserMemory(userID, agent, "profile", string(content)); err != nil {
			slog.Error("memory extraction: failed to upsert profile", "error", err)
		} else {
			slog.Info("memory extraction: updated profile", "user", userID)
		}
	}

	// Store topic updates as memory items and rebuild the topics summary.
	if len(result.TopicUpdates) > 0 {
		for _, tu := range result.TopicUpdates {
			if tu.Topic == "" || tu.Summary == "" {
				continue
			}
			content := tu.Summary
			if len(tu.Details) > 0 {
				content += "\n- " + strings.Join(tu.Details, "\n- ")
			}
			tags := strings.Join(tu.Tags, ",")
			if _, err := s.store.InsertMemoryItem(MemoryItem{
				UserID:  userID,
				Agent:   agent,
				Topic:   tu.Topic,
				Content: content,
				Tags:    tags,
			}); err != nil {
				slog.Error("memory extraction: failed to insert memory item", "error", err, "topic", tu.Topic)
			} else {
				slog.Info("memory extraction: stored topic update", "user", userID, "topic", tu.Topic)
			}
		}

		// Rebuild topics summary layer.
		s.updateTopicsSummary(userID, agent)
	}

	// Upsert notes if we got updates.
	if result.NotesUpdates != nil {
		existing := findLayer(memories, "notes")
		merged := mergeProfile(existing, result.NotesUpdates)
		content, _ := json.Marshal(merged)
		if err := s.store.UpsertUserMemory(userID, agent, "notes", string(content)); err != nil {
			slog.Error("memory extraction: failed to upsert notes", "error", err)
		} else {
			slog.Info("memory extraction: updated notes", "user", userID)
		}
	}
}

// updateTopicsSummary aggregates distinct topics from memory_items into a
// summary JSON object and upserts it to user_memory with layer="topics".
func (s *Server) updateTopicsSummary(userID, agent string) {
	// Search with empty query to get all items, then deduplicate by topic.
	items, err := s.store.SearchMemoryItems(userID, agent, "", 100)
	if err != nil {
		slog.Error("memory extraction: failed to list memory items for summary", "error", err)
		return
	}

	// Build topic → latest summary map.
	topics := make(map[string]string)
	for _, item := range items {
		if item.Topic == "" {
			continue
		}
		// Keep the most recent entry per topic (items are ordered by updated_at DESC).
		if _, exists := topics[item.Topic]; !exists {
			// Truncate to first line / 120 chars for summary.
			summary := item.Content
			if idx := strings.Index(summary, "\n"); idx > 0 {
				summary = summary[:idx]
			}
			if len(summary) > 120 {
				summary = summary[:120] + "..."
			}
			topics[item.Topic] = summary
		}
	}

	if len(topics) == 0 {
		return
	}

	content, _ := json.Marshal(topics)
	if err := s.store.UpsertUserMemory(userID, agent, "topics", string(content)); err != nil {
		slog.Error("memory extraction: failed to upsert topics summary", "error", err)
	}
}

// formatMemoryForInjection formats stored memories into text for the system prompt.
func formatMemoryForInjection(memories []UserMemory) string {
	if len(memories) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Memory\n")

	for _, m := range memories {
		switch m.Layer {
		case "profile":
			b.WriteString("\n### Who they are\n")
			b.WriteString(formatProfileContent(m.Content))
		case "topics":
			b.WriteString("\n### Active topics\n")
			b.WriteString(formatTopicsContent(m.Content))
		case "journal":
			// Legacy backward compat: render existing journal entries read-only.
			b.WriteString("\n### History (legacy)\n")
			b.WriteString(formatJournalContent(m.Content))
		case "notes":
			b.WriteString("\n### Notes\n")
			b.WriteString(formatNotesContent(m.Content))
		}
	}

	b.WriteString("\nReference this context naturally. Don't recite it mechanically.")
	b.WriteString("\nYou have memory tools: use `recall` to search past details, `remember` to save important info.")
	return b.String()
}

// formatTopicsContent renders the topics summary as one-liners.
func formatTopicsContent(content string) string {
	var topics map[string]string
	if err := json.Unmarshal([]byte(content), &topics); err != nil {
		return content
	}
	var b strings.Builder
	for topic, summary := range topics {
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", topic, summary))
	}
	b.WriteString("(Use `recall` tool for full details on any topic.)\n")
	return b.String()
}

// formatProfileContent converts profile JSON to readable text.
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

// journalEntry is a legacy coaching session summary (kept for backward compat).
type journalEntry struct {
	Date           string   `json:"date"`
	Challenge      string   `json:"challenge"`
	Advice         string   `json:"advice"`
	ActionItems    []string `json:"action_items"`
	FrameworksUsed []string `json:"frameworks_used"`
}

// formatJournalContent converts journal JSON array to readable text.
func formatJournalContent(content string) string {
	var entries []journalEntry
	if err := json.Unmarshal([]byte(content), &entries); err != nil {
		return content
	}

	// Show only the most recent 10.
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

// formatNotesContent converts notes JSON to readable text.
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

// buildExistingMemoryJSON builds a JSON representation of existing memories.
func buildExistingMemoryJSON(memories []UserMemory) string {
	if len(memories) == 0 {
		return "{}"
	}
	m := make(map[string]json.RawMessage)
	for _, mem := range memories {
		m[mem.Layer] = json.RawMessage(mem.Content)
	}
	data, _ := json.Marshal(m)
	return string(data)
}

// buildExtractionPrompt constructs the prompt for the extraction LLM.
func buildExtractionPrompt(existingMemory, userMsg, agentResponse string) string {
	return fmt.Sprintf(`You are analyzing a conversation to extract memory updates.

EXISTING MEMORY:
%s

LATEST EXCHANGE:
User: %s
Agent: %s

Extract ONLY new or changed information. Return JSON:
{
  "profile_updates": {"key": "value", ...} or null,
  "topic_updates": [{"topic": "...", "summary": "...", "details": ["..."], "tags": ["..."]}] or null,
  "notes_updates": {"key": "value", ...} or null
}

Rules:
- profile_updates: factual info about the person (name, business, role, location, etc.)
- topic_updates: projects, tasks, ongoing discussions. Each needs a clear topic name, a one-line summary, optional detail bullets, and tags for search. Only create entries for substantive topics discussed, not casual chat.
- notes_updates: communication preferences, personality observations, recurring themes
- If nothing meaningful was revealed, return {"profile_updates":null,"topic_updates":null,"notes_updates":null}
- Return ONLY valid JSON, no markdown fences, no explanation.`, existingMemory, userMsg, agentResponse)
}

// parseExtractionResult extracts the JSON from the LLM response.
func parseExtractionResult(content string) (*extractionResult, error) {
	// Strip markdown code fences if present.
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result extractionResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("unmarshal extraction result: %w", err)
	}
	return &result, nil
}

// findLayer returns the content of a specific layer, or empty string.
func findLayer(memories []UserMemory, layer string) string {
	for _, m := range memories {
		if m.Layer == layer {
			return m.Content
		}
	}
	return ""
}

// mergeProfile merges new updates into an existing profile JSON.
func mergeProfile(existingContent string, updates map[string]any) map[string]any {
	existing := make(map[string]any)
	if existingContent != "" {
		json.Unmarshal([]byte(existingContent), &existing)
	}
	for k, v := range updates {
		existing[k] = v
	}
	return existing
}
