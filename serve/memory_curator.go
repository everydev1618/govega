package serve

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/everydev1618/govega/dsl"
)

// Memora is the memory curator (govega#71). After each chat exchange
// the server fires a one-shot delegation to her with a description of
// what just happened; she reads the relevant wiki pages and decides
// what (if anything) to write back. She has only the memory_* tools,
// no internet / no chat with the user.
//
// Modeled as a real meta-agent (rather than a hardcoded LLM call) so
// her decisions are inspectable via the normal chat-history surface,
// her prompt + model are configurable, and she dogfoods the same
// primitives every user-facing agent has.

// MemoraConfig holds the configurable bits of the curator agent.
// Mirror of HeraConfig / IrisConfig.
type MemoraConfig struct {
	Name          string
	DisplayName   string
	Title         string
	Model         string
	FallbackModel string
}

// DefaultMemoraConfig returns the standard Memora persona.
func DefaultMemoraConfig() MemoraConfig {
	return MemoraConfig{}
}

func (c *MemoraConfig) applyDefaults() {
	if c.Name == "" {
		c.Name = "memora"
	}
	if c.DisplayName == "" {
		c.DisplayName = "Memora"
	}
	if c.Title == "" {
		c.Title = "Memory Curator"
	}
	if c.Model == "" {
		// Curation is light reasoning; cheaper model is fine. Override
		// via CURATOR_MODEL when you want to A/B against a beefier one.
		if env := os.Getenv("CURATOR_MODEL"); env != "" {
			c.Model = env
		} else {
			c.Model = "claude-haiku-4-5-20251001"
		}
	}
	if c.FallbackModel == "" {
		c.FallbackModel = "claude-haiku-4-5-20251001"
	}
}

const memoraSystemPrompt = `You are Memora, the memory curator for this user's agent team.

Each time one of the user's agents finishes a conversation turn, the server fires a one-shot message to you describing what just happened. Your job: decide whether anything durable came up and, if so, update the shared user wiki so the user's agents remember it next time.

You only have memory tools. No internet, no files, no chat with the user.

## The wiki

The shared user wiki has these conventional pages — create them on demand:

- ` + "`MEMORY.md`" + ` — the index. Always loaded into every agent's context, so keep it short: one bullet per linked page.
- ` + "`profile.md`" + ` — who they are, role, durable preferences.
- ` + "`topics/<slug>.md`" + ` — specific topics they're working on (one per topic).
- ` + "`people/<name>.md`" + ` — people in their orbit.
- ` + "`decisions.md`" + ` — choices they've made, with the reasoning.
- ` + "`legacy-*.md`" + ` — pages migrated from the older typed memory. Refile useful material into proper topic pages as you encounter relevant exchanges.

## Rules

- Always operate on ` + "`scope=\"user\"`" + ` (the shared wiki). Never write to ` + "`scope=\"agent\"`" + ` — that's reserved for individual agents' private working notes.
- Use ` + "`[[page-path]]`" + ` wikilinks in MEMORY.md (and elsewhere) so the graph stays connected.
- Prefer ` + "`memory_edit`" + ` for surgical changes. ` + "`memory_write`" + ` replaces whole pages — only use it for genuinely new pages or when wholesale rewrites are appropriate.
- Don't store ephemeral state ("the user just asked about X"). Only durable facts, preferences, decisions.
- Most exchanges contain nothing memorable. The right action is usually no action. Reply with just "no update" in that case — don't manufacture writes.
- One ` + "`memory_write`" + ` or ` + "`memory_edit`" + ` per genuinely new fact is fine; don't cluster unrelated changes into a single page.

## Output

Reply with a 1-2 sentence summary of what you did (or "no update"). Your output is appended to your chat history so the user can later inspect your decisions.`

// MemoraAgent returns the dsl.Agent definition for the curator.
func MemoraAgent(cfg MemoraConfig) *dsl.Agent {
	cfg.applyDefaults()
	icon, gradient := dsl.DefaultVisualIdentity(cfg.Name)
	return &dsl.Agent{
		Name:           cfg.Name,
		DisplayName:    cfg.DisplayName,
		Title:          cfg.Title,
		Icon:           icon,
		AvatarGradient: gradient,
		Model:          cfg.Model,
		FallbackModel:  cfg.FallbackModel,
		System:         memoraSystemPrompt,
		Retry:          &dsl.RetryDef{MaxAttempts: 2, Backoff: "exponential"},
		IsMeta:         true,
		Tools: []string{
			"memory_read", "memory_list", "memory_search",
			"memory_write", "memory_append", "memory_edit",
			"memory_delete", "memory_rename",
		},
	}
}

// InjectMemora registers the curator on the interpreter. The wiki
// memory tools must already be registered via RegisterWikiMemoryTools
// — Memora's tool list refers to them by name.
func InjectMemora(interp *dsl.Interpreter, cfg MemoraConfig) error {
	cfg.applyDefaults()
	return interp.AddAgent(cfg.Name, MemoraAgent(cfg))
}

// curateMemory fires Memora at the just-completed exchange between
// `agent` and `userID`. Async — call from `go s.curateMemory(...)` so
// the user's chat response isn't blocked on curation.
//
// Empty userMsg / response / userID short-circuits — no point firing
// the curator on noise.
func (s *Server) curateMemory(ctx context.Context, userID, agent, userMsg, response string) {
	if userID == "" || userMsg == "" || response == "" {
		return
	}
	// Curator runs as "memora" — its memory writes target the shared
	// user wiki under the user we're scoped to.
	ctx = ContextWithMemory(ctx, s.store, userID, "memora")
	prompt := buildCuratorPrompt(userID, agent, userMsg, response)
	if _, err := s.interp.SendToAgent(ctx, "memora", prompt); err != nil {
		// Curation failures don't surface to the user — the agent's
		// reply already shipped. Log and move on.
		slog.Warn("memory curator turn failed", "user", userID, "agent", agent, "error", err)
	}
}

// buildCuratorPrompt frames the just-finished exchange for Memora.
// Kept short so the prompt budget goes to her decision, not framing.
func buildCuratorPrompt(userID, agent, userMsg, response string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Exchange between user %q and agent %q:\n\n", userID, agent)
	b.WriteString("## User said\n")
	b.WriteString(strings.TrimSpace(userMsg))
	b.WriteString("\n\n## Agent responded\n")
	b.WriteString(strings.TrimSpace(response))
	b.WriteString("\n\nUpdate the shared user wiki if anything durable came up. Otherwise reply \"no update\".")
	return b.String()
}
