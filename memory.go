package vega

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Memory provides persistent storage for agent knowledge.
type Memory interface {
	// Store saves a value with a key
	Store(key string, value any, metadata map[string]any) error

	// Retrieve performs semantic search and returns top-k results
	Retrieve(query string, k int) ([]MemoryItem, error)

	// Get retrieves a specific item by key
	Get(key string) (MemoryItem, error)

	// Delete removes an item
	Delete(key string) error
}

// MemoryItem is a stored memory entry.
type MemoryItem struct {
	Key       string
	Value     any
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
	Score     float64 // Relevance score for Retrieve
}

// ContextManager manages conversation history and token budgets.
type ContextManager interface {
	// Add appends a message to the context
	Add(msg Message)

	// Messages returns messages that fit within maxTokens
	Messages(maxTokens int) []Message

	// Clear resets the context
	Clear()

	// TokenCount returns current token usage
	TokenCount() int
}

// CompactableContext extends ContextManager with compaction support.
// Compaction summarizes old messages to reduce token usage while preserving context.
type CompactableContext interface {
	ContextManager

	// Compact summarizes older messages to reduce token count.
	// The LLM is used to generate summaries.
	Compact(llm LLM) error

	// NeedsCompaction returns true if the context exceeds the threshold.
	NeedsCompaction(threshold int) bool
}

// SlidingWindowContext implements ContextManager with a sliding window and optional compaction.
type SlidingWindowContext struct {
	messages       []Message
	maxMessages    int
	compactedCount int
	summary        string
	mu             sync.RWMutex
}

// NewSlidingWindowContext creates a context manager with a sliding window.
// maxMessages is the number of recent messages to keep (0 = unlimited).
func NewSlidingWindowContext(maxMessages int) *SlidingWindowContext {
	return &SlidingWindowContext{
		messages:    make([]Message, 0),
		maxMessages: maxMessages,
	}
}

// Add appends a message to the context.
func (c *SlidingWindowContext) Add(msg Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, msg)

	// If we have a max and exceed it, remove oldest messages
	if c.maxMessages > 0 && len(c.messages) > c.maxMessages {
		// Keep only the most recent maxMessages
		excess := len(c.messages) - c.maxMessages
		c.messages = c.messages[excess:]
		c.compactedCount += excess
	}
}

// Messages returns messages that fit within maxTokens.
func (c *SlidingWindowContext) Messages(maxTokens int) []Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Message, 0, len(c.messages)+1)

	// Include summary as system message if we have one
	if c.summary != "" {
		result = append(result, Message{
			Role:    RoleSystem,
			Content: "Previous conversation summary:\n" + c.summary,
		})
	}

	// Add messages in order
	result = append(result, c.messages...)

	return result
}

// Clear resets the context.
func (c *SlidingWindowContext) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = make([]Message, 0)
	c.compactedCount = 0
	c.summary = ""
}

// TokenCount returns an estimated token count (roughly 4 chars per token).
func (c *SlidingWindowContext) TokenCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := 0
	for _, msg := range c.messages {
		total += len(msg.Content) / 4
	}
	if c.summary != "" {
		total += len(c.summary) / 4
	}
	return total
}

// NeedsCompaction returns true if token count exceeds threshold.
func (c *SlidingWindowContext) NeedsCompaction(threshold int) bool {
	return c.TokenCount() > threshold
}

// Compact summarizes older messages using the provided LLM.
func (c *SlidingWindowContext) Compact(llm LLM) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Need at least 4 messages to compact (keep recent 2, summarize rest)
	if len(c.messages) < 4 {
		return nil
	}

	// Split messages: older half to summarize, recent half to keep
	splitPoint := len(c.messages) / 2
	toSummarize := c.messages[:splitPoint]
	toKeep := c.messages[splitPoint:]

	// Build summarization prompt
	var content strings.Builder
	content.WriteString("Please provide a brief summary of this conversation excerpt, focusing on key decisions, facts, and context that would be important for continuing the conversation:\n\n")
	for _, msg := range toSummarize {
		content.WriteString(string(msg.Role))
		content.WriteString(": ")
		content.WriteString(msg.Content)
		content.WriteString("\n\n")
	}

	// Call LLM to generate summary
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := llm.Generate(ctx, []Message{
		{Role: RoleUser, Content: content.String()},
	}, nil)
	if err != nil {
		return err
	}

	// Combine with existing summary if present
	if c.summary != "" {
		c.summary = c.summary + "\n\n" + resp.Content
	} else {
		c.summary = resp.Content
	}

	// Keep only recent messages
	c.messages = toKeep
	c.compactedCount += splitPoint

	return nil
}
