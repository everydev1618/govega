package memory

import (
	"encoding/json"
	"sync"

	"github.com/everydev1618/govega/llm"
)

// TokenBudgetContext manages conversation history within a token budget.
// When adding messages would exceed the budget, oldest messages are removed.
type TokenBudgetContext struct {
	messages   []llm.Message
	maxTokens  int
	tokenCount int
	mu         sync.RWMutex
}

// NewTokenBudgetContext creates a context that keeps messages within a token budget.
// Uses ~4 chars per token as a rough estimate.
func NewTokenBudgetContext(maxTokens int) *TokenBudgetContext {
	return &TokenBudgetContext{
		messages:  make([]llm.Message, 0),
		maxTokens: maxTokens,
	}
}

// estimateTokens estimates token count for a message (~4 chars per token).
func estimateTokens(content string) int {
	return (len(content) + 3) / 4 // Round up
}

// Add appends a message to the context.
// If the new message would exceed the budget, oldest messages are removed —
// but only at user-message boundaries (see trimToBudgetLocked).
func (c *TokenBudgetContext) Add(msg llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, msg)
	c.tokenCount += estimateTokens(msg.Content)

	c.trimToBudgetLocked()
}

// trimToBudgetLocked drops oldest messages until the context fits the
// budget, cutting only at user-message boundaries: history handed to the
// API must start a fresh turn, and a window that opens on an assistant
// message (or mid tool exchange) is an unrecoverable 400. If no safe
// boundary exists, the context is left over budget — validity beats the
// token target. Caller must hold c.mu.
func (c *TokenBudgetContext) trimToBudgetLocked() {
	if c.tokenCount <= c.maxTokens || len(c.messages) <= 1 {
		return
	}

	// Walk from the front: the smallest prefix whose removal gets us
	// under budget, then extend the cut to the next user message.
	dropped := 0
	cut := 0
	for cut < len(c.messages)-1 && c.tokenCount-dropped > c.maxTokens {
		dropped += estimateTokens(c.messages[cut].Content)
		cut++
	}
	for cut < len(c.messages) && c.messages[cut].Role != llm.RoleUser {
		dropped += estimateTokens(c.messages[cut].Content)
		cut++
	}
	if cut == 0 || cut >= len(c.messages) {
		return // no safe trim point — try again on the next append
	}

	c.messages = append([]llm.Message(nil), c.messages[cut:]...)
	c.tokenCount -= dropped
}

// RemoveLastIf removes the most recent message if it matches the given role
// and content, returning whether a message was removed. Callers use this to
// roll back a user message whose LLM turn failed, keeping the history free
// of orphaned user messages.
func (c *TokenBudgetContext) RemoveLastIf(role llm.Role, content string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := len(c.messages)
	if n == 0 {
		return false
	}
	last := c.messages[n-1]
	if last.Role != role || last.Content != content {
		return false
	}
	c.messages = c.messages[:n-1]
	c.tokenCount -= estimateTokens(last.Content)
	return true
}

// Messages returns messages that fit within maxTokens.
// If the requested maxTokens is lower than our budget, we return fewer messages.
func (c *TokenBudgetContext) Messages(maxTokens int) []llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Respect the requested maxTokens (may be lower than our budget)
	effectiveMax := c.maxTokens
	if maxTokens > 0 && maxTokens < effectiveMax {
		effectiveMax = maxTokens
	}

	// Find the earliest start index whose suffix fits the limit.
	start := len(c.messages)
	tokens := 0
	for i := len(c.messages) - 1; i >= 0; i-- {
		msgTokens := estimateTokens(c.messages[i].Content)
		if tokens+msgTokens > effectiveMax {
			break
		}
		tokens += msgTokens
		start = i
	}

	// Advance to a user-message boundary so the window starts a fresh
	// turn — a window opening on an assistant message is API-invalid.
	boundary := start
	for boundary < len(c.messages) && c.messages[boundary].Role != llm.RoleUser {
		boundary++
	}
	if boundary >= len(c.messages) {
		boundary = start // degenerate: no user turn fits — better than empty
	}

	result := make([]llm.Message, len(c.messages)-boundary)
	copy(result, c.messages[boundary:])
	return result
}

// Clear resets the context.
func (c *TokenBudgetContext) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = c.messages[:0]
	c.tokenCount = 0
}

// TokenCount returns current token usage.
func (c *TokenBudgetContext) TokenCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokenCount
}

// Load initializes the context with existing messages.
// This is useful for restoring conversation history from persistence.
// If the loaded messages exceed the budget, oldest messages are trimmed.
func (c *TokenBudgetContext) Load(messages []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = make([]llm.Message, 0, len(messages))
	c.tokenCount = 0

	for _, msg := range messages {
		c.messages = append(c.messages, msg)
		c.tokenCount += estimateTokens(msg.Content)
	}

	// Trim if loaded messages exceed budget — at user boundaries only.
	c.trimToBudgetLocked()
}

// Snapshot returns a copy of all messages for persistence.
func (c *TokenBudgetContext) Snapshot() []llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]llm.Message, len(c.messages))
	copy(result, c.messages)
	return result
}

// MarshalMessages serializes messages to JSON.
func MarshalMessages(messages []llm.Message) ([]byte, error) {
	return json.Marshal(messages)
}

// UnmarshalMessages deserializes messages from JSON.
func UnmarshalMessages(data []byte) ([]llm.Message, error) {
	var messages []llm.Message
	err := json.Unmarshal(data, &messages)
	return messages, err
}
