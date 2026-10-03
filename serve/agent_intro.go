package serve

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
)

// IntroPrompt is the synthetic user turn sent to a newly-created agent to
// generate its first assistant message (the "introduce yourself" turn from
// govega#63). Held in a var rather than a const so a tenant-specific config
// surface can override it later without an API change.
var IntroPrompt = "Introduce yourself to the user in 2-3 sentences. Tell them who you are, what your role is, and the kinds of tasks you can help with. Be warm and concise. This is the first message they'll see from you."

// DefaultIntroTimeout bounds the synthetic greeting turn when
// VEGA_INTRO_TIMEOUT says nothing. It is deliberately as long as the LLM
// clients' own HTTP ceiling: this bound exists to stop a wedged goroutine
// from living forever, not to race the backend. The previous 60s raced it
// and lost against self-hosted models — a cold 27B on LM Studio can spend
// ~44s on prefill alone, and when the greeting lost that race the process
// was marked failed, so the user's first real turn came back "process is
// not running". Nothing waits on this goroutine, so patience is cheap.
const DefaultIntroTimeout = 5 * time.Minute

// introTimeout resolves the greeting-turn bound. VEGA_INTRO_TIMEOUT takes a
// Go duration ("90s", "10m"); anything unparseable or non-positive falls
// back to the default rather than producing an already-expired context,
// which would be indistinguishable from a backend that never answers.
func introTimeout() time.Duration {
	v := os.Getenv("VEGA_INTRO_TIMEOUT")
	if v == "" {
		return DefaultIntroTimeout
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("ignoring unusable VEGA_INTRO_TIMEOUT; using default",
			"value", v, "default", DefaultIntroTimeout)
		return DefaultIntroTimeout
	}
	return d
}

// chatSender is the slice of *dsl.Interpreter that primeAgentIntro depends
// on. Keeping the surface narrow lets tests inject a fake without standing
// up an LLM backend.
type chatSender interface {
	SendToAgent(ctx context.Context, agent, message string) (string, error)
}

// chatHistoryStore is the slice of Store that primeAgentIntro depends on.
type chatHistoryStore interface {
	ListChatMessages(agent string) ([]ChatMessage, error)
	InsertChatMessage(agent, role, content string, activities []vega.ToolActivity) error
}

// primeAgentIntro runs one synthetic assistant turn against `agent` and
// persists the response as the first chat message. It is the implementation
// of the contract laid out in govega#63: newly-created agents should not
// land users on a blank chat screen.
//
// Behavior pinned by tests in agent_intro_test.go:
//   - Idempotent: if the agent already has any chat messages, return without
//     calling the LLM or persisting. This guards the bootstrap path (server
//     restart re-runs Iris/Hera injection) and the HTTP-retry case.
//   - Failure-soft: LLM errors are logged and swallowed. Agent creation
//     never fails because the intro turn timed out.
//   - Persists only the assistant message. The synthetic user prompt is
//     intentionally not in chat history — the UI shows a clean greeting,
//     not a fake user message. hydrateAgent re-injects the prompt into the
//     in-process buffer when needed so the LLM context stays valid.
func primeAgentIntro(ctx context.Context, store chatHistoryStore, sender chatSender, agent string) {
	history, err := store.ListChatMessages(agent)
	if err == nil && len(history) > 0 {
		return
	}
	response, err := sender.SendToAgent(ctx, agent, IntroPrompt)
	if err != nil {
		slog.Warn("agent intro turn failed; agent created without greeting", "agent", agent, "error", err)
		return
	}
	if err := store.InsertChatMessage(agent, "assistant", response, nil); err != nil {
		slog.Warn("failed to persist agent intro message", "agent", agent, "error", err)
	}
}

// primeAgentIntroAsync runs primeAgentIntro in a goroutine with a bounded
// timeout. Used by HTTP handlers and Hera's create_agent callback so the
// creation response isn't gated on LLM latency (the intro will surface in
// the FE's next chat-history poll / SSE update).
func (s *Server) primeAgentIntroAsync(agent string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), introTimeout())
		defer cancel()
		primeAgentIntro(ctx, s.store, s.interp, agent)
	}()
}

// buildHydrationMessages converts persisted chat messages into the
// in-process LLM message buffer. The only non-1:1 case is when the first
// persisted row is an assistant message — produced by primeAgentIntro and
// rejected by Anthropic as a conversation opener. We prepend a synthetic
// user message (the same IntroPrompt that produced the assistant turn) so
// the live and rehydrated buffers are byte-identical from the LLM's POV.
func buildHydrationMessages(history []ChatMessage) []llm.Message {
	msgs := make([]llm.Message, 0, len(history)+1)
	if len(history) > 0 && history[0].Role == "assistant" {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: IntroPrompt})
	}
	for _, m := range history {
		role := llm.RoleUser
		if m.Role == "assistant" {
			role = llm.RoleAssistant
		}
		msgs = append(msgs, llm.Message{Role: role, Content: m.Content})
	}
	return msgs
}
