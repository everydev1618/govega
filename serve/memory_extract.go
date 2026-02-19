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

const maxJournalEntries = 20
const displayJournalEntries = 10

// extractionResult is the JSON structure returned by the extraction LLM.
type extractionResult struct {
	ProfileUpdates map[string]any `json:"profile_updates"`
	JournalEntry   *journalEntry  `json:"journal_entry"`
	NotesUpdates   map[string]any `json:"notes_updates"`
}

// journalEntry is a single coaching session summary.
type journalEntry struct {
	Date           string   `json:"date"`
	Challenge      string   `json:"challenge"`
	Advice         string   `json:"advice"`
	ActionItems    []string `json:"action_items"`
	FrameworksUsed []string `json:"frameworks_used"`
}

// extractMemory runs an async LLM call to extract memory from the latest exchange.
func (s *Server) extractMemory(userID, agent, userMsg, response string) {
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

	// Append journal entry if present.
	if result.JournalEntry != nil {
		if result.JournalEntry.Date == "" {
			result.JournalEntry.Date = time.Now().Format("2006-01-02")
		}
		existing := findLayer(memories, "journal")
		var entries []journalEntry
		if existing != "" {
			json.Unmarshal([]byte(existing), &entries)
		}
		entries = append(entries, *result.JournalEntry)
		// Cap at maxJournalEntries.
		if len(entries) > maxJournalEntries {
			entries = entries[len(entries)-maxJournalEntries:]
		}
		content, _ := json.Marshal(entries)
		if err := s.store.UpsertUserMemory(userID, agent, "journal", string(content)); err != nil {
			slog.Error("memory extraction: failed to upsert journal", "error", err)
		} else {
			slog.Info("memory extraction: added journal entry", "user", userID)
		}
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

// formatMemoryForInjection formats stored memories into text for the system prompt.
func formatMemoryForInjection(memories []UserMemory) string {
	if len(memories) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Coaching Client Memory\n")

	for _, m := range memories {
		switch m.Layer {
		case "profile":
			b.WriteString("\n### Who they are\n")
			b.WriteString(formatProfileContent(m.Content))
		case "journal":
			b.WriteString("\n### Coaching history (recent)\n")
			b.WriteString(formatJournalContent(m.Content))
		case "notes":
			b.WriteString("\n### Notes\n")
			b.WriteString(formatNotesContent(m.Content))
		}
	}

	b.WriteString("\nReference this context naturally. Don't recite it mechanically. Check in on past action items when relevant.")
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

// formatJournalContent converts journal JSON array to readable text.
func formatJournalContent(content string) string {
	var entries []journalEntry
	if err := json.Unmarshal([]byte(content), &entries); err != nil {
		return content
	}

	// Show only the most recent displayJournalEntries.
	start := 0
	if len(entries) > displayJournalEntries {
		start = len(entries) - displayJournalEntries
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
func buildExtractionPrompt(existingMemory, userMsg, danResponse string) string {
	return fmt.Sprintf(`You are analyzing a coaching conversation to extract memory updates.

EXISTING MEMORY:
%s

LATEST EXCHANGE:
User: %s
Dan: %s

Extract ONLY new or changed information. Return JSON:
{
  "profile_updates": {"key": "value", ...} or null,
  "journal_entry": {"challenge": "...", "advice": "...", "action_items": [...], "frameworks_used": [...]} or null,
  "notes_updates": {"key": "value", ...} or null
}

Rules:
- profile_updates: factual info about the person (name, business, role, location, revenue, employees, etc.)
- journal_entry: only if a meaningful coaching exchange happened (challenge discussed, advice given, commitments made)
- notes_updates: communication preferences, personality observations, recurring themes
- If nothing meaningful was revealed, return {"profile_updates":null,"journal_entry":null,"notes_updates":null}
- Return ONLY valid JSON, no markdown fences, no explanation.`, existingMemory, userMsg, danResponse)
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
