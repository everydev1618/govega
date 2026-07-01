package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/tools"
)

// canonicalAgentName returns the stable lowercase identity used as the
// `agent` column in memory storage. Strips any ":<process-id>" suffix
// (a runtime leak — refs #79) and lowercases the remainder. Without
// this, an orchestrator renamed from Iris → Charlie writes memories
// under four different identities and can never recall its own past
// (refs #83).
func canonicalAgentName(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

// memoryContextKey is a type for memory context keys.
type memoryContextKey string

const (
	memCtxStore  memoryContextKey = "memory.store"
	memCtxUserID memoryContextKey = "memory.userID"
	memCtxAgent  memoryContextKey = "memory.agent"
)

// ContextWithMemory returns a context carrying the store, userID, and agent
// needed by the memory tools (remember, recall, forget).
func ContextWithMemory(ctx context.Context, store Store, userID, agent string) context.Context {
	ctx = context.WithValue(ctx, memCtxStore, store)
	ctx = context.WithValue(ctx, memCtxUserID, userID)
	ctx = context.WithValue(ctx, memCtxAgent, canonicalAgentName(agent))
	return ctx
}

// memoryFromContext extracts store, userID, agent from context.
func memoryFromContext(ctx context.Context) (Store, string, string, error) {
	store, _ := ctx.Value(memCtxStore).(Store)
	userID, _ := ctx.Value(memCtxUserID).(string)
	agent, _ := ctx.Value(memCtxAgent).(string)
	if store == nil || userID == "" || agent == "" {
		return nil, "", "", fmt.Errorf("memory context not set")
	}
	return store, userID, agent, nil
}

// RegisterMemoryTools registers remember, recall, and forget tools on the
// interpreter's global tool collection.
func RegisterMemoryTools(interp *dsl.Interpreter) {
	t := interp.Tools()

	t.Register("remember", tools.ToolDef{
		Description: "Save information to long-term memory. Always specify the `type` so retrieval can target the right slot — `user` (stable facts about who they are and how they like to work), `feedback` (corrections/confirmations on your behavior), `project` (context about ongoing work), `reference` (pointers to external systems and docs). If you write the same content under the same type twice, the second call merges tags into the first row instead of creating a duplicate.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}

			content, _ := params["content"].(string)
			if content == "" {
				return "", fmt.Errorf("content is required")
			}
			topic, _ := params["topic"].(string)
			tags, _ := params["tags"].(string)

			typ := MemoryTypeReference
			if rawType, ok := params["type"].(string); ok && rawType != "" {
				candidate := MemoryType(rawType)
				if err := candidate.Validate(); err != nil {
					return "", err
				}
				typ = candidate
			}

			id, err := store.InsertMemoryItem(MemoryItem{
				UserID:  userID,
				Agent:   agent,
				Type:    typ,
				Topic:   topic,
				Content: content,
				Tags:    tags,
			})
			if err != nil {
				return "", fmt.Errorf("save memory: %w", err)
			}

			// Emit onto the spine so agents can react to a memory being written.
			interp.PublishEvent(events.Event{
				Type: "memory.wrote",
				Data: map[string]any{"agent": agent, "type": string(typ), "topic": topic},
			})

			return fmt.Sprintf("Saved to memory (id=%d, type=%q, topic=%q).", id, typ, topic), nil
		}),
		Params: map[string]tools.ParamDef{
			"content": {
				Type:        "string",
				Description: "The information to remember",
				Required:    true,
			},
			"type": {
				Type:        "string",
				Description: "Memory class: user | feedback | project | reference. Defaults to 'reference' if omitted, but always set this explicitly for new memories so future recall can target the right slot.",
			},
			"topic": {
				Type:        "string",
				Description: "Topic or project name to file this under (e.g. 'Dan's project', 'marketing strategy')",
			},
			"tags": {
				Type:        "string",
				Description: "Comma-separated tags for easier retrieval (e.g. 'dan,api,backend')",
			},
		},
	})

	t.Register("recall", tools.ToolDef{
		Description: "Search long-term memory by keyword. Pass `type` to narrow the search to a single class (user/feedback/project/reference) — useful when you want, say, only feedback rules and not project trivia.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, userID, agent, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}

			query, _ := params["query"].(string)
			if query == "" {
				return "", fmt.Errorf("query is required")
			}

			limit := 10
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var (
				items []MemoryItem
			)
			if rawType, ok := params["type"].(string); ok && rawType != "" {
				candidate := MemoryType(rawType)
				if err := candidate.Validate(); err != nil {
					return "", err
				}
				items, err = store.SearchMemoryItemsByType(userID, agent, query, candidate, limit)
			} else {
				items, err = store.SearchMemoryItems(userID, agent, query, limit)
			}
			if err != nil {
				return "", fmt.Errorf("search memory: %w", err)
			}

			if len(items) == 0 {
				return "No memories found matching that query.", nil
			}

			type result struct {
				ID      int64      `json:"id"`
				Type    MemoryType `json:"type"`
				Topic   string     `json:"topic,omitempty"`
				Content string     `json:"content"`
				Tags    string     `json:"tags,omitempty"`
				Date    string     `json:"date"`
			}

			results := make([]result, len(items))
			for i, item := range items {
				results[i] = result{
					ID:      item.ID,
					Type:    item.Type,
					Topic:   item.Topic,
					Content: item.Content,
					Tags:    item.Tags,
					Date:    item.CreatedAt.Format("2006-01-02"),
				}
			}

			out, _ := json.MarshalIndent(results, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"query": {
				Type:        "string",
				Description: "Keyword or phrase to search for across topics, content, and tags",
				Required:    true,
			},
			"type": {
				Type:        "string",
				Description: "Optional: narrow to a single memory class (user/feedback/project/reference)",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of results (default 10)",
			},
		},
	})

	t.Register("forget", tools.ToolDef{
		Description: "Delete a specific memory by its ID. Use recall first to find the ID of the memory to delete.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, _, _, err := memoryFromContext(ctx)
			if err != nil {
				return "", err
			}

			// Accept id as either float64 (JSON number) or string.
			var id int64
			switch v := params["id"].(type) {
			case float64:
				id = int64(v)
			case string:
				id, err = strconv.ParseInt(v, 10, 64)
				if err != nil {
					return "", fmt.Errorf("invalid id: %w", err)
				}
			default:
				return "", fmt.Errorf("id is required")
			}

			if err := store.DeleteMemoryItem(id); err != nil {
				return "", fmt.Errorf("delete memory: %w", err)
			}

			return fmt.Sprintf("Memory %d deleted.", id), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {
				Type:        "number",
				Description: "ID of the memory to delete (use recall to find IDs)",
				Required:    true,
			},
		},
	})
}
