package vega

import (
	"time"
)

// Agent defines an AI agent. It's a blueprint, not a running process.
// Spawn an Agent with an Orchestrator to get a running Process.
type Agent struct {
	// Name is a human-readable identifier for this agent
	Name string

	// Model is the LLM model ID (e.g., "claude-sonnet-4-20250514")
	Model string

	// System is the system prompt (static or dynamic)
	System SystemPrompt

	// Tools available to this agent
	Tools *Tools

	// Memory provides persistent storage (optional)
	Memory Memory

	// Context manages conversation history (optional)
	Context ContextManager

	// Budget sets cost limits (optional)
	Budget *Budget

	// Retry configures retry behavior for transient failures (optional)
	Retry *RetryPolicy

	// RateLimit throttles requests (optional)
	RateLimit *RateLimit

	// CircuitBreaker isolates failures (optional)
	CircuitBreaker *CircuitBreaker

	// LLM is the backend to use (optional, uses default if not set)
	LLM LLM

	// Temperature for generation (0.0-1.0, optional)
	Temperature *float64

	// MaxTokens limits response length (optional)
	MaxTokens int
}

// SystemPrompt provides the system prompt for an agent.
// It can be static (StaticPrompt) or dynamic (DynamicPrompt).
type SystemPrompt interface {
	Prompt() string
}

// StaticPrompt is a fixed system prompt string.
type StaticPrompt string

// Prompt returns the static prompt string.
func (s StaticPrompt) Prompt() string {
	return string(s)
}

// DynamicPrompt is a function that generates a system prompt.
// It's called each turn, allowing the prompt to include current state.
type DynamicPrompt func() string

// Prompt calls the function to generate the prompt.
func (d DynamicPrompt) Prompt() string {
	return d()
}

// Budget configures cost limits for an agent.
type Budget struct {
	// Limit is the maximum cost in USD
	Limit float64

	// OnExceed determines behavior when budget is exceeded
	OnExceed BudgetAction
}

// BudgetAction determines what happens when a budget is exceeded.
type BudgetAction int

const (
	// BudgetBlock prevents the request from executing
	BudgetBlock BudgetAction = iota

	// BudgetWarn logs a warning but allows the request
	BudgetWarn

	// BudgetAllow silently allows the request
	BudgetAllow
)

// RetryPolicy configures retry behavior for transient failures.
type RetryPolicy struct {
	// MaxAttempts is the maximum number of retry attempts
	MaxAttempts int

	// Backoff configures delay between retries
	Backoff BackoffConfig

	// RetryOn specifies which error classes to retry
	RetryOn []ErrorClass
}

// BackoffConfig configures retry delays.
type BackoffConfig struct {
	// Initial delay before first retry
	Initial time.Duration

	// Multiplier for exponential backoff
	Multiplier float64

	// Max delay between retries
	Max time.Duration

	// Jitter adds randomness (0.0-1.0)
	Jitter float64

	// Type of backoff (linear, exponential, constant)
	Type BackoffType
}

// BackoffType specifies the backoff algorithm.
type BackoffType int

const (
	BackoffExponential BackoffType = iota
	BackoffLinear
	BackoffConstant
)

// ErrorClass categorizes errors for retry decisions.
type ErrorClass int

const (
	ErrClassRateLimit ErrorClass = iota
	ErrClassOverloaded
	ErrClassTimeout
	ErrClassTemporary
	ErrClassInvalidRequest
	ErrClassAuthentication
	ErrClassBudgetExceeded
)

// RateLimit configures request throttling.
type RateLimit struct {
	// RequestsPerMinute limits request rate
	RequestsPerMinute int

	// TokensPerMinute limits token throughput
	TokensPerMinute int
}

// CircuitBreaker isolates failures to prevent cascading.
type CircuitBreaker struct {
	// Threshold is failures before opening the circuit
	Threshold int

	// ResetAfter is time before trying again (half-open)
	ResetAfter time.Duration

	// HalfOpenMax is requests allowed in half-open state
	HalfOpenMax int

	// OnOpen is called when circuit opens
	OnOpen func()

	// OnClose is called when circuit closes
	OnClose func()
}

// Message represents a conversation message.
type Message struct {
	Role    Role
	Content string
}

// Role identifies the message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
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
