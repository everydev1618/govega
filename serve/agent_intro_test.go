package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/everydev1618/govega/llm"
)

// fakeChatSender records the prompt it was asked to send and returns a canned
// response (or error). Lets us exercise primeAgentIntro without an LLM.
type fakeChatSender struct {
	response string
	err      error

	calls       int
	lastAgent   string
	lastMessage string
}

func (f *fakeChatSender) SendToAgent(ctx context.Context, agent, message string) (string, error) {
	f.calls++
	f.lastAgent = agent
	f.lastMessage = message
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

// TestPrimeAgentIntro_PersistsAssistantMessage pins the happy path: with no
// prior chat history, primeAgentIntro runs one synthetic turn and persists
// the assistant response. The introduction prompt itself is NOT persisted —
// it would clutter the UI and contradict the spec on govega#63 ("role:
// assistant, no special flagging").
func TestPrimeAgentIntro_PersistsAssistantMessage(t *testing.T) {
	store := newTestStore(t)
	sender := &fakeChatSender{response: "Hi, I'm Aria, your orchestrator."}

	primeAgentIntro(context.Background(), store, sender, "aria")

	if sender.calls != 1 {
		t.Fatalf("sender.calls = %d, want 1", sender.calls)
	}
	if sender.lastAgent != "aria" {
		t.Errorf("sender.lastAgent = %q, want %q", sender.lastAgent, "aria")
	}
	if sender.lastMessage != IntroPrompt {
		t.Errorf("sender.lastMessage = %q, want IntroPrompt", sender.lastMessage)
	}

	history, err := store.ListChatMessages("aria")
	if err != nil {
		t.Fatalf("ListChatMessages: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1 (only the assistant intro should be persisted)", len(history))
	}
	if history[0].Role != "assistant" {
		t.Errorf("history[0].Role = %q, want %q", history[0].Role, "assistant")
	}
	if history[0].Content != sender.response {
		t.Errorf("history[0].Content = %q, want %q", history[0].Content, sender.response)
	}
}

// TestPrimeAgentIntro_IdempotentWhenHistoryExists guards the retry / boot-time
// case: if the agent already has chat messages, do not re-introduce. Without
// this, restarting the server would double-introduce every agent.
func TestPrimeAgentIntro_IdempotentWhenHistoryExists(t *testing.T) {
	store := newTestStore(t)
	if err := store.InsertChatMessage("aria", "user", "hello", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sender := &fakeChatSender{response: "should not be called"}

	primeAgentIntro(context.Background(), store, sender, "aria")

	if sender.calls != 0 {
		t.Errorf("sender.calls = %d, want 0 (history exists — must not re-intro)", sender.calls)
	}
	history, _ := store.ListChatMessages("aria")
	if len(history) != 1 {
		t.Errorf("history len = %d, want 1 (must not insert a second message)", len(history))
	}
}

// TestPrimeAgentIntro_SwallowsSenderError pins the failure mode the issue
// explicitly calls out: "if the introduction turn fails (model timeout etc.),
// don't fail agent creation. Log and continue." We assert the function
// returns without panicking AND does not persist a malformed assistant
// message containing the error string.
func TestPrimeAgentIntro_SwallowsSenderError(t *testing.T) {
	store := newTestStore(t)
	sender := &fakeChatSender{err: errors.New("llm: rate limit")}

	primeAgentIntro(context.Background(), store, sender, "aria")

	history, _ := store.ListChatMessages("aria")
	if len(history) != 0 {
		t.Errorf("history len = %d, want 0 (sender errored — must not persist)", len(history))
	}
}

// TestHydrateAgent_AssistantFirstPrependsSyntheticUser pins the LLM-context
// invariant that complements the spec: chat_messages may start with an
// `assistant` row (the intro), but Anthropic rejects a conversation whose
// first message is `assistant`. hydrateAgent must therefore prepend a
// synthetic user message so the rehydrated process buffer is a valid LLM
// conversation. The synthetic message is the same intro prompt that
// produced the assistant turn in the first place, keeping live and
// rehydrated buffers byte-identical.
func TestHydrateAgent_AssistantFirstPrependsSyntheticUser(t *testing.T) {
	got := buildHydrationMessages([]ChatMessage{
		{Role: "assistant", Content: "Hi, I'm Aria."},
		{Role: "user", Content: "What's on my plate today?"},
		{Role: "assistant", Content: "You have 3 tasks open."},
	})

	if len(got) != 4 {
		t.Fatalf("hydrated len = %d, want 4 (synthetic user + 3 persisted)", len(got))
	}
	if got[0].Role != llm.RoleUser || got[0].Content != IntroPrompt {
		t.Errorf("got[0] = {%v, %q}, want {user, IntroPrompt}", got[0].Role, got[0].Content)
	}
	if got[1].Role != llm.RoleAssistant || got[1].Content != "Hi, I'm Aria." {
		t.Errorf("got[1] = {%v, %q}, want {assistant, intro}", got[1].Role, got[1].Content)
	}
}

// TestHydrateAgent_UserFirstUnchanged confirms the prepend logic only kicks
// in when needed — a normal conversation starting with a user message
// hydrates 1:1.
func TestHydrateAgent_UserFirstUnchanged(t *testing.T) {
	got := buildHydrationMessages([]ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	})

	if len(got) != 2 {
		t.Fatalf("hydrated len = %d, want 2 (no prepend for user-first)", len(got))
	}
	if got[0].Role != llm.RoleUser || got[0].Content != "hi" {
		t.Errorf("got[0] = {%v, %q}", got[0].Role, got[0].Content)
	}
}

// TestIntroTimeout_Default pins the bound used when nothing is configured.
// The old value was 60s, which a cold local model could not clear: a 27B on
// LM Studio spent ~44s on prefill alone for a cold prefix, so the greeting
// turn failed the process and the first real chat turn 500'd with "process
// is not running". The bound exists to stop a wedged goroutine, not to race
// the backend, so it sits at the LLM client's own 5-minute ceiling.
func TestIntroTimeout_Default(t *testing.T) {
	t.Setenv("VEGA_INTRO_TIMEOUT", "")
	if got := introTimeout(); got != DefaultIntroTimeout {
		t.Fatalf("introTimeout() = %v, want %v", got, DefaultIntroTimeout)
	}
	if DefaultIntroTimeout < 5*time.Minute {
		t.Fatalf("DefaultIntroTimeout = %v, want at least the 5m LLM client ceiling", DefaultIntroTimeout)
	}
}

// TestIntroTimeout_EnvOverride lets a slow (or fast) backend tune the bound
// without a rebuild — the knob a self-hosted deployment actually reaches for.
func TestIntroTimeout_EnvOverride(t *testing.T) {
	t.Setenv("VEGA_INTRO_TIMEOUT", "90s")
	if got := introTimeout(); got != 90*time.Second {
		t.Fatalf("introTimeout() = %v, want 90s", got)
	}
}

// TestIntroTimeout_RejectsUnusableValues keeps a typo from silently producing
// an already-expired context, which would look exactly like a backend that
// never answers.
func TestIntroTimeout_RejectsUnusableValues(t *testing.T) {
	for _, v := range []string{"banana", "0", "-30s"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("VEGA_INTRO_TIMEOUT", v)
			if got := introTimeout(); got != DefaultIntroTimeout {
				t.Fatalf("introTimeout() = %v for %q, want the default %v", got, v, DefaultIntroTimeout)
			}
		})
	}
}
