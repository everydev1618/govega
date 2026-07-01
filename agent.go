package vega

import (
	"time"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/memory"
	"github.com/everydev1618/govega/reactive"
	"github.com/everydev1618/govega/tools"
)

// Agent defines an AI agent. It's a blueprint, not a running process.
// Spawn an Agent with an Orchestrator to get a running Process.
type Agent struct {
	// Name is a human-readable identifier for this agent
	Name string

	// Model is the LLM model ID (e.g., "claude-sonnet-4-6")
	Model string

	// FallbackModel is used when all retries with the primary model are exhausted (optional)
	FallbackModel string

	// Models is an optional per-step-type routing table. Keys are
	// step-type tags chosen by the runtime (e.g. "classify", "code",
	// "summarize"); values are model IDs. ModelFor reads it; a miss
	// returns Model.
	Models map[string]string

	// System is the system prompt (static or dynamic)
	System SystemPrompt

	// Tools available to this agent
	Tools *tools.Tools

	// Memory provides persistent storage (optional)
	Memory memory.Memory

	// Context manages conversation history (optional)
	Context memory.ContextManager

	// Budget sets cost limits (optional)
	Budget *Budget

	// Retry configures retry behavior for transient failures (optional)
	Retry *RetryPolicy

	// RateLimit throttles requests (optional)
	RateLimit *RateLimit

	// CircuitBreaker isolates failures (optional)
	CircuitBreaker *CircuitBreaker

	// LLM is the backend to use (optional, uses default if not set)
	LLM llm.LLM

	// Temperature for generation (0.0-1.0, optional). Silently dropped
	// for backends/models that don't support sampling parameters
	// (e.g. Claude Opus 4.7).
	Temperature *float64

	// MaxTokens limits response length (optional). 0 = use the
	// backend's per-model default (capability table).
	MaxTokens int

	// Effort controls thinking depth and overall token spend on
	// supported Claude models — "low" | "medium" | "high" | "xhigh"
	// | "max". Empty means "high" by default. Use "xhigh" for
	// agentic and coding workloads on Opus 4.7. "max" is Opus-tier
	// only. Silently dropped for backends/models that don't support
	// effort (e.g. Sonnet 4.5, Haiku 4.5).
	Effort string

	// MaxIterations limits tool call loop iterations (default: DefaultMaxIterations)
	MaxIterations int

	// Triggers are the events this agent reacts to. Reactivity is the third
	// declarative faculty alongside System (personality) and Memory; it lives
	// on the blueprint so an Agent is a complete description of a reactive
	// entity. Empty means the agent only runs when explicitly invoked. See
	// docs/reactive-agents-design.md (decision D1).
	Triggers []reactive.Trigger
}

// Default configuration values
const (
	// DefaultMaxIterations is the default maximum tool call loop iterations.
	// Engineering tasks (build, run, curl, fix, retry) routinely chain
	// 50+ tool calls; the previous cap of 50 was hitting "maximum
	// iterations exceeded" on real work. 100 is a more realistic
	// default; agents that genuinely need more set Agent.MaxIterations.
	DefaultMaxIterations = 100

	// DefaultMaxContextTokens is the default context window size
	DefaultMaxContextTokens = 100000

	// DefaultLLMTimeout is the default timeout for LLM API calls
	DefaultLLMTimeout = 5 * time.Minute

	// DefaultStreamBufferSize is the default buffer size for streaming responses
	DefaultStreamBufferSize = 100

	// DefaultSupervisorPollInterval is the default interval for supervisor health checks
	DefaultSupervisorPollInterval = 100 * time.Millisecond
)

// ModelFor returns the model to use for a given step-type tag. If the
// agent has a Models map entry for the role, that wins; otherwise it
// returns the agent's primary Model. An empty role always falls back.
func (a Agent) ModelFor(role string) string {
	if role != "" {
		if m, ok := a.Models[role]; ok && m != "" {
			return m
		}
	}
	return a.Model
}

// fallbackOptions returns the llm.Options to use when retrying with
// FallbackModel after the primary model has exhausted retries. The
// agent's temperature, max_tokens, and effort carry over — only the
// model swaps.
func (a Agent) fallbackOptions() llm.Options {
	return llm.Options{
		Model:       a.FallbackModel,
		Temperature: a.Temperature,
		MaxTokens:   a.MaxTokens,
		Effort:      a.Effort,
	}
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

