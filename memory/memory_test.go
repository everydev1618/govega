package memory

import (
	"context"
	"testing"

	"github.com/everydev1618/govega/llm"
)

// --- SlidingWindowContext Tests ---

func TestSlidingWindowContext_Add(t *testing.T) {
	ctx := NewSlidingWindowContext(5)

	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "hello"})
	ctx.Add(llm.Message{Role: llm.RoleAssistant, Content: "hi"})

	msgs := ctx.Messages(100000)
	if len(msgs) != 2 {
		t.Errorf("Messages count = %d, want 2", len(msgs))
	}
}

func TestSlidingWindowContext_SlidingWindow(t *testing.T) {
	ctx := NewSlidingWindowContext(3)

	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "msg1"})
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "msg2"})
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "msg3"})
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "msg4"}) // Should evict msg1

	msgs := ctx.Messages(100000)
	if len(msgs) != 3 {
		t.Fatalf("Messages count = %d, want 3", len(msgs))
	}
	if msgs[0].Content != "msg2" {
		t.Errorf("First message = %q, want %q", msgs[0].Content, "msg2")
	}
	if msgs[2].Content != "msg4" {
		t.Errorf("Last message = %q, want %q", msgs[2].Content, "msg4")
	}
}

func TestSlidingWindowContext_UnlimitedWindow(t *testing.T) {
	ctx := NewSlidingWindowContext(0) // Unlimited

	for i := 0; i < 100; i++ {
		ctx.Add(llm.Message{Role: llm.RoleUser, Content: "msg"})
	}

	msgs := ctx.Messages(100000)
	if len(msgs) != 100 {
		t.Errorf("Messages count = %d, want 100", len(msgs))
	}
}

func TestSlidingWindowContext_Clear(t *testing.T) {
	ctx := NewSlidingWindowContext(10)

	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "hello"})
	ctx.Clear()

	msgs := ctx.Messages(100000)
	if len(msgs) != 0 {
		t.Errorf("After Clear, messages count = %d, want 0", len(msgs))
	}
	if ctx.TokenCount() != 0 {
		t.Errorf("After Clear, TokenCount = %d, want 0", ctx.TokenCount())
	}
}

func TestSlidingWindowContext_TokenCount(t *testing.T) {
	ctx := NewSlidingWindowContext(10)

	// ~4 chars per token, "hello" = 5 chars => ~1 token
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "hello world 1234"})

	tc := ctx.TokenCount()
	if tc == 0 {
		t.Error("TokenCount should be > 0")
	}

	// 16 chars / 4 = 4 tokens
	if tc != 4 {
		t.Errorf("TokenCount = %d, want 4", tc)
	}
}

func TestSlidingWindowContext_NeedsCompaction(t *testing.T) {
	ctx := NewSlidingWindowContext(0)

	// Add a long message
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "a very long message that exceeds the threshold for sure"})

	if !ctx.NeedsCompaction(1) {
		t.Error("Should need compaction with threshold 1")
	}
	if ctx.NeedsCompaction(100000) {
		t.Error("Should not need compaction with very high threshold")
	}
}

func TestSlidingWindowContext_Compact(t *testing.T) {
	ctx := NewSlidingWindowContext(0)

	// Add enough messages for compaction (needs at least 4)
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "first message"})
	ctx.Add(llm.Message{Role: llm.RoleAssistant, Content: "first reply"})
	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "second message"})
	ctx.Add(llm.Message{Role: llm.RoleAssistant, Content: "second reply"})

	mockLLM := &compactMockLLM{response: "Summary: first exchange discussed greetings"}

	err := ctx.Compact(mockLLM)
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}

	// Should have summary + remaining messages
	msgs := ctx.Messages(100000)
	if len(msgs) < 1 {
		t.Fatal("Should have messages after compaction")
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Error("First message should be system (summary)")
	}
	if msgs[0].Content == "" {
		t.Error("Summary should not be empty")
	}
}

func TestSlidingWindowContext_CompactTooFewMessages(t *testing.T) {
	ctx := NewSlidingWindowContext(0)

	ctx.Add(llm.Message{Role: llm.RoleUser, Content: "hello"})
	ctx.Add(llm.Message{Role: llm.RoleAssistant, Content: "hi"})

	mockLLM := &compactMockLLM{response: "should not be called"}

	err := ctx.Compact(mockLLM)
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}

	// Should still have 2 messages (compaction skipped)
	msgs := ctx.Messages(100000)
	if len(msgs) != 2 {
		t.Errorf("Messages count = %d, want 2 (compaction should be skipped)", len(msgs))
	}
}

func TestSlidingWindowContext_CompactAccumulatesSummaries(t *testing.T) {
	ctx := NewSlidingWindowContext(0)

	// Add 8 messages for two rounds of compaction
	for i := 0; i < 8; i++ {
		ctx.Add(llm.Message{Role: llm.RoleUser, Content: "message"})
	}

	mockLLM := &compactMockLLM{response: "Summary 1"}
	ctx.Compact(mockLLM)

	// Add more messages
	for i := 0; i < 4; i++ {
		ctx.Add(llm.Message{Role: llm.RoleUser, Content: "message"})
	}

	mockLLM.response = "Summary 2"
	ctx.Compact(mockLLM)

	msgs := ctx.Messages(100000)
	// First message should be combined summaries
	if msgs[0].Role != llm.RoleSystem {
		t.Error("First message should be system (combined summary)")
	}
}

// --- SlidingWindowContext implements ContextManager ---

func TestSlidingWindowContext_ImplementsContextManager(t *testing.T) {
	var _ ContextManager = (*SlidingWindowContext)(nil)
}

func TestSlidingWindowContext_ImplementsCompactableContext(t *testing.T) {
	var _ CompactableContext = (*SlidingWindowContext)(nil)
}

// compactMockLLM is a mock for testing compaction.
type compactMockLLM struct {
	response string
}

func (m *compactMockLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: m.response}, nil
}

func (m *compactMockLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Delta: m.response}
	close(ch)
	return ch, nil
}
