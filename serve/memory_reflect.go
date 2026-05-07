package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/llm"
)

// reflectedMemoryEntry is one memory the reflection LLM proposes saving.
// The shape mirrors MemoryItem's writable fields plus a Type discriminator
// (string at the wire so model output is forgiving; validated server-side).
type reflectedMemoryEntry struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Topic   string `json:"topic,omitempty"`
	Tags    string `json:"tags,omitempty"`
}

// reflectionResult is the JSON shape the reflection LLM returns.
type reflectionResult struct {
	Memories []reflectedMemoryEntry `json:"memories"`
}

// reflectionSchema enforces the response shape on models that support
// structured outputs. Unknown fields are rejected at the top level so a
// model that decides to chat instead of returning JSON fails fast.
var reflectionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"memories": map[string]any{
			"type": []string{"array", "null"},
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
					"topic":   map[string]any{"type": "string"},
					"tags":    map[string]any{"type": "string"},
				},
				"required":             []string{"type", "content"},
				"additionalProperties": false,
			},
		},
	},
	"additionalProperties": false,
}

// agentReflectEnabled reports whether reflection should fire for an agent
// whose YAML carries the supplied MemoryDef (nil if the agent has no memory
// block). The global VEGA_REFLECTION env var still acts as an override:
//
//   - "false" / "0" / "no": reflection off for everyone (kill switch).
//   - "true" / "1" / "yes": reflection on for everyone (global on).
//   - unset or unrecognised: defer to the per-agent flag (default off).
//
// The kill-switch path is the safety valve operators reach for when an
// agent under load is generating noisy reflection writes; the global-on
// path is convenient during development before per-agent flags are tuned.
func agentReflectEnabled(memoryDef *dsl.MemoryDef) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("VEGA_REFLECTION")))
	switch v {
	case "false", "0", "no":
		return false
	case "true", "1", "yes":
		return true
	}
	return memoryDef != nil && memoryDef.Reflect
}

// reflectionInsertStore is the subset of Store the reflection runner needs.
// Narrowing the surface keeps the test seam small.
type reflectionInsertStore interface {
	InsertMemoryItem(item MemoryItem) (int64, error)
}

// runReflection asks ll to propose typed memories worth keeping from the
// latest userMsg/response exchange and writes them via store.InsertMemoryItem.
// Each entry's Type is validated; invalid entries are skipped so a single
// bad row doesn't poison the whole run. Returns a non-nil error only when
// the LLM call or JSON parse fails — write errors are logged per-entry but
// do not abort the run.
func runReflection(ctx context.Context, ll llm.LLM, store reflectionInsertStore, userID, agent, userMsg, response string) error {
	if ll == nil {
		return fmt.Errorf("reflection: nil llm")
	}
	if store == nil {
		return fmt.Errorf("reflection: nil store")
	}

	prompt := buildReflectionPrompt(userMsg, response)
	ctx = llm.ContextWithOptions(ctx, llm.Options{OutputSchema: reflectionSchema})

	resp, err := ll.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return fmt.Errorf("reflection llm call: %w", err)
	}

	result, err := parseReflectionResult(resp.Content)
	if err != nil {
		return fmt.Errorf("reflection parse: %w", err)
	}

	for _, entry := range result.Memories {
		typ := MemoryType(entry.Type)
		if err := typ.Validate(); err != nil {
			slog.Warn("reflection: skipping entry with invalid type",
				"agent", agent, "type", entry.Type, "error", err)
			continue
		}
		if strings.TrimSpace(entry.Content) == "" {
			slog.Warn("reflection: skipping entry with empty content", "agent", agent, "type", typ)
			continue
		}
		if _, err := store.InsertMemoryItem(MemoryItem{
			UserID:  userID,
			Agent:   agent,
			Type:    typ,
			Topic:   entry.Topic,
			Content: entry.Content,
			Tags:    entry.Tags,
		}); err != nil {
			slog.Error("reflection: insert failed",
				"agent", agent, "type", typ, "error", err)
			continue
		}
	}
	return nil
}

// parseReflectionResult parses the LLM's response, tolerating markdown fences
// some models still emit even with structured output enforcement.
func parseReflectionResult(content string) (*reflectionResult, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var r reflectionResult
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return nil, fmt.Errorf("unmarshal reflection result: %w", err)
	}
	return &r, nil
}

// buildReflectionPrompt produces the instruction sent to the reflection LLM.
// The four-class schema (user/feedback/project/reference) mirrors the typed
// memory introduced in the previous PR so reflected entries land in the
// right slots for retrieval.
func buildReflectionPrompt(userMsg, agentResponse string) string {
	return fmt.Sprintf(`You are reflecting on a single agent turn to decide what (if anything) is worth saving as long-term memory.

LATEST EXCHANGE:
User: %s
Agent: %s

Return JSON of the form:
{
  "memories": [
    {"type": "user|feedback|project|reference", "content": "...", "topic": "...", "tags": "comma,separated"}
  ]
}

Type guide:
- "user": stable facts about who the user is, their role, how they like to work. Save when the user reveals something durable about themselves.
- "feedback": corrections or confirmations about the agent's behavior ("don't do X", "the way you did Y was right"). Save when the user signals what to keep or stop doing.
- "project": context about ongoing work, decisions, deadlines, motivations behind the current task. Save when project state shifts or a non-obvious decision is made.
- "reference": pointers to external systems and documents (URLs, dashboard names, ticket IDs).

Rules:
- Only save what was actually revealed in this exchange — don't invent facts.
- Skip casual chat, greetings, and ephemeral task details (those belong in the conversation, not memory).
- Prefer fewer, denser memories over many shallow ones. If nothing is worth saving, return {"memories": []}.
- Each "content" should be a single declarative sentence the future agent can act on.
- Return ONLY valid JSON, no markdown fences, no commentary.`, userMsg, agentResponse)
}

// reflectMemory is the Server-bound wrapper used by chat handlers. It runs
// runReflection in the background using the server's existing extract LLM
// and store, with a hard timeout so a stuck inference can't pin a goroutine
// forever. Failures are logged; nothing is propagated to the caller.
//
// The agent's per-agent memory config (if any) is consulted alongside the
// global VEGA_REFLECTION env var to decide whether to fire — see
// agentReflectEnabled for the resolution rules.
func (s *Server) reflectMemory(userID, agent, userMsg, response string) {
	if !agentReflectEnabled(s.lookupAgentMemory(agent)) {
		return
	}
	ll := s.getExtractLLM()
	if ll == nil {
		slog.Debug("reflection: no llm available, skipping")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runReflection(ctx, ll, s.store, userID, agent, userMsg, response); err != nil {
		slog.Warn("reflection: run failed", "agent", agent, "error", err)
	}
}

// lookupAgentMemory returns the parsed MemoryDef from the agent's YAML
// definition, or nil if the agent has no memory block (or no entry in
// the document, e.g. composed runtime agents that haven't been
// re-parsed). The chat handler may pass an agent name with a session
// suffix (e.g. "apex:abc"); the suffix is stripped before lookup.
func (s *Server) lookupAgentMemory(agent string) *dsl.MemoryDef {
	if i := strings.Index(agent, ":"); i >= 0 {
		agent = agent[:i]
	}
	doc := s.interp.Document()
	if doc == nil {
		return nil
	}
	def, ok := doc.Agents[agent]
	if !ok || def == nil {
		return nil
	}
	return def.Memory
}
