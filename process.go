package vega

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/everydev1618/govega/llm"
)

// contextKey is a type for context keys used by vega.
type contextKey string

// processContextKey is the context key for the current process.
const processContextKey contextKey = "vega.process"

// eventSinkContextKey is the context key for a ChatEvent channel that
// receives nested tool activity from delegated agent calls.
const eventSinkContextKey contextKey = "vega.event_sink"

// ContextWithEventSink returns a new context with a ChatEvent sink attached.
func ContextWithEventSink(ctx context.Context, ch chan<- ChatEvent) context.Context {
	return context.WithValue(ctx, eventSinkContextKey, ch)
}

// EventSinkFromContext retrieves the ChatEvent sink from the context, if present.
func EventSinkFromContext(ctx context.Context) chan<- ChatEvent {
	ch, _ := ctx.Value(eventSinkContextKey).(chan<- ChatEvent)
	return ch
}

// ContextWithProcess returns a new context with the process attached.
func ContextWithProcess(ctx context.Context, p *Process) context.Context {
	return context.WithValue(ctx, processContextKey, p)
}

// ProcessFromContext retrieves the process from the context, if present.
func ProcessFromContext(ctx context.Context) *Process {
	p, _ := ctx.Value(processContextKey).(*Process)
	return p
}

// Process is a running Agent with state and lifecycle.
type Process struct {
	// ID is the unique identifier for this process
	ID string

	// Agent is the agent definition this process is running
	Agent *Agent

	// Task describes what this process is working on
	Task string

	// WorkDir is the isolated workspace directory
	WorkDir string

	// Project is the container project name for isolated execution
	Project string

	// StartedAt is when the process was spawned
	StartedAt time.Time

	// Supervision configures fault tolerance
	Supervision *Supervision

	// status is the current process state
	status Status

	// metrics tracks usage
	metrics ProcessMetrics

	// context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// messages is the conversation history
	messages []llm.Message

	// iteration count
	iteration int

	// maxIterations overrides the agent's per-turn tool-loop cap when > 0
	// (set via WithMaxIterations at spawn time).
	maxIterations int

	// turnToolSigs counts identical (tool, args, result) signatures within the
	// current turn. When one signature repeats thrashRepeatThreshold times the
	// agent is looping without progress and the circuit breaker trips. Reset at
	// the start of each tool loop.
	turnToolSigs map[string]int

	// turnTimeout optionally overrides DefaultTurnTimeout for this process's
	// turns. 0 => use DefaultTurnTimeout.
	turnTimeout time.Duration

	// llm is the backend to use
	llm llm.LLM

	// orchestrator reference for child spawning
	orchestrator *Orchestrator

	// mutex for thread safety
	mu sync.RWMutex

	// sendMu serializes conversation turns (Send/SendStream/SendStreamRich).
	// Concurrent turns on one process would interleave user/assistant
	// messages and run overlapping LLM calls against the same history.
	sendMu sync.Mutex

	// finalResult stores the result when process completes
	finalResult string

	// Process linking (Erlang-style)
	// links are bidirectional - if linked process dies, we die too (unless trapExit)
	links map[string]*Process
	// monitors are unidirectional - we get notified when monitored process dies
	monitors map[string]*monitorEntry
	// monitoredBy tracks who is monitoring us (for cleanup)
	monitoredBy map[string]*monitorEntry
	// trapExit when true, converts exit signals to messages instead of killing
	trapExit bool
	// exitSignals receives exit notifications when trapExit is true
	exitSignals chan ExitSignal
	// linkMu protects link/monitor maps
	linkMu sync.RWMutex
	// nextMonitorID for generating unique monitor references
	nextMonitorID uint64

	// Named process support
	name string

	// Per-agent rate limiter and circuit breaker (initialized on first use)
	rateLimiter    *agentRateLimiter
	circuitBreaker *circuitBreakerState

	// Automatic restart support. restartPolicySet distinguishes an
	// explicitly configured policy from the zero value — ChildRestart's
	// zero value is Permanent, and treating "unset" as Permanent would
	// auto-restart every failed process whose agent is registered
	// (double-restarting children a Supervisor already manages).
	restartPolicy    ChildRestart
	restartPolicySet bool
	spawnOpts        []SpawnOption

	// extraSystem is additional system prompt content injected per-process.
	extraSystem string

	// Process group membership
	groups map[string]*ProcessGroup

	// Spawn tree tracking
	ParentID    string   // ID of spawning process (empty if root)
	ParentAgent string   // Agent name of parent
	ChildIDs    []string // Child process IDs
	childMu     sync.RWMutex
	SpawnDepth  int    // Depth in tree (0 = root)
	SpawnReason string // Task/context for spawn
}

// Status represents the process lifecycle state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
)

// ProcessMetrics tracks process usage.
type ProcessMetrics struct {
	Iterations               int
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	CostUSD                  float64
	StartedAt                time.Time
	CompletedAt              time.Time
	LastActiveAt             time.Time
	ToolCalls                int
	Errors                   int
}

// SendResult is the result of a Send operation.
type SendResult struct {
	Response string
	Error    error
	Metrics  CallMetrics
}

// CallMetrics tracks a single LLM call.
type CallMetrics struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	CostUSD                  float64
	LatencyMs                int64
	ToolCalls                []string
	Retries                  int
}

// Status returns the current process status.
func (p *Process) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// isTerminal reports whether the process has reached a terminal state.
func (p *Process) isTerminal() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return isTerminalStatus(p.status)
}

// isTerminalStatus reports whether a status is terminal (no further work).
func isTerminalStatus(s Status) bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusTimeout
}

// effectiveMaxIterations returns the per-turn tool-loop cap, honoring a
// spawn-level override (WithMaxIterations), then the agent's setting, then
// the package default.
func (p *Process) effectiveMaxIterations() int {
	if p.maxIterations > 0 {
		return p.maxIterations
	}
	if p.Agent != nil && p.Agent.MaxIterations > 0 {
		return p.Agent.MaxIterations
	}
	return DefaultMaxIterations
}

// Metrics returns the current process metrics.
func (p *Process) Metrics() ProcessMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.metrics
}

// Name returns the registered name of the process, or empty string if not named.
func (p *Process) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.name
}

// Groups returns the names of all groups this process belongs to.
func (p *Process) Groups() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, 0, len(p.groups))
	for name := range p.groups {
		names = append(names, name)
	}
	return names
}

// SetExtraSystem sets additional system prompt content that is appended
// after the main system prompt. Use this to inject per-process context
// (e.g. user memory) without modifying the agent's shared System prompt.
func (p *Process) SetExtraSystem(content string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.extraSystem = content
}

// ExtraSystem returns the currently-set extra system content (empty if none).
func (p *Process) ExtraSystem() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.extraSystem
}

// DefaultTurnTimeout bounds a single conversation turn (one Send/SendStream/
// SendStreamRich) when neither the caller's context nor the process sets a
// tighter deadline. It is the backstop against a stalled LLM stream or a hung
// tool call becoming an unbounded wait — e.g. a Discord/Telegram bot passes
// context.Background(), so without this a stuck turn shows "typing…" forever.
var DefaultTurnTimeout = 10 * time.Minute

// SetTurnTimeout overrides the per-turn wall-clock deadline for this process.
// A value <= 0 restores the package default (DefaultTurnTimeout).
func (p *Process) SetTurnTimeout(d time.Duration) {
	p.mu.Lock()
	p.turnTimeout = d
	p.mu.Unlock()
}

// effectiveTurnTimeout returns this process's per-turn deadline, falling back
// to the package default.
func (p *Process) effectiveTurnTimeout() time.Duration {
	p.mu.RLock()
	tt := p.turnTimeout
	p.mu.RUnlock()
	if tt <= 0 {
		return DefaultTurnTimeout
	}
	return tt
}

// withProcessCancel derives a context from the caller's ctx that is also
// cancelled when the process itself is cancelled — via Stop/Complete/Fail/Kill,
// a linked-process death cascade, or orchestrator Shutdown. This makes those
// lifecycle events actually abort in-flight LLM calls, tool execution, and
// retry/backoff sleeps instead of letting them run to completion. The returned
// cancel must be called (defer) to release the watcher goroutine.
func (p *Process) withProcessCancel(ctx context.Context) (context.Context, context.CancelFunc) {
	// Per-turn wall-clock backstop. Callers that never bound the turn (the
	// Discord/Telegram bots pass context.Background()) would otherwise let a
	// stalled LLM stream or hung tool call run forever. A caller-supplied
	// deadline is respected as-is and never loosened.
	timeoutCancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		if tt := p.effectiveTurnTimeout(); tt > 0 {
			ctx, timeoutCancel = context.WithTimeout(ctx, tt)
		}
	}

	merged, mergedCancel := context.WithCancel(ctx)
	cancel := func() { mergedCancel(); timeoutCancel() }

	if p.ctx != nil {
		go func() {
			select {
			case <-p.ctx.Done():
				cancel()
			case <-merged.Done():
			}
		}()
	}
	return merged, cancel
}

// Send sends a message and waits for a response.
// Turns are serialized per process: a Send that arrives while another turn
// is in flight waits for it to finish rather than interleaving history.
func (p *Process) Send(ctx context.Context, message string) (string, error) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	p.mu.Lock()
	if p.status != StatusRunning && p.status != StatusPending {
		p.mu.Unlock()
		return "", ErrProcessNotRunning
	}
	p.status = StatusRunning
	p.iteration++
	p.metrics.LastActiveAt = time.Now()
	p.mu.Unlock()

	// Abort the loop if the process itself is cancelled, not just the caller.
	ctx, cancel := p.withProcessCancel(ctx)
	defer cancel()

	// Add user message to context
	p.addMessage(llm.Message{Role: llm.RoleUser, Content: message})

	// Execute the LLM call loop (may involve tool calls)
	response, callMetrics, err := p.executeLLMLoop(ctx, message)
	if err != nil {
		// The turn produced no assistant reply; leaving the user message in
		// place would make the next turn send two consecutive user messages.
		p.rollbackUserMessage(message)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Context cancelled or timed out — fail the process so ensureAgent
			// can respawn it cleanly on the next call rather than leaving it
			// stuck in StatusRunning forever.
			p.Fail(err)
		} else {
			p.mu.Lock()
			p.metrics.Errors++
			p.mu.Unlock()
		}
		return "", err
	}

	// Update metrics
	p.mu.Lock()
	p.metrics.InputTokens += callMetrics.InputTokens
	p.metrics.OutputTokens += callMetrics.OutputTokens
	p.metrics.CacheCreationInputTokens += callMetrics.CacheCreationInputTokens
	p.metrics.CacheReadInputTokens += callMetrics.CacheReadInputTokens
	p.metrics.CostUSD += callMetrics.CostUSD
	p.metrics.ToolCalls += len(callMetrics.ToolCalls)
	p.mu.Unlock()

	// Add assistant response to context
	p.addMessage(llm.Message{Role: llm.RoleAssistant, Content: response})

	return response, nil
}

// SendAsync sends a message and returns a Future.
func (p *Process) SendAsync(message string) *Future {
	f := &Future{
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
	}

	go func() {
		ctx, cancel := context.WithCancel(context.Background())

		// Handle cancellation
		go func() {
			select {
			case <-f.cancel:
				cancel()
			case <-f.done:
			}
		}()

		result, err := p.Send(ctx, message)
		f.mu.Lock()
		f.result = result
		f.err = err
		f.completed = true
		f.mu.Unlock()
		close(f.done)
	}()

	return f
}

// SendStream sends a message and returns a streaming response.
func (p *Process) SendStream(ctx context.Context, message string) (*Stream, error) {
	p.mu.Lock()
	if p.status != StatusRunning && p.status != StatusPending {
		p.mu.Unlock()
		return nil, ErrProcessNotRunning
	}
	p.status = StatusRunning
	p.iteration++
	p.metrics.LastActiveAt = time.Now()
	p.mu.Unlock()

	// Create stream
	stream := &Stream{
		chunks: make(chan string, DefaultStreamBufferSize),
		done:   make(chan struct{}),
	}

	// Execute streaming in goroutine. The user message is appended inside
	// the goroutine, once the turn holds sendMu — appending it here would
	// let concurrent stream sends interleave their user messages.
	go func() {
		p.sendMu.Lock()
		defer p.sendMu.Unlock()

		ctx, cancel := p.withProcessCancel(ctx)
		defer cancel()
		defer close(stream.chunks)
		defer close(stream.done)

		p.addMessage(llm.Message{Role: llm.RoleUser, Content: message})

		response, err := p.executeLLMStream(ctx, message, stream.chunks)
		stream.mu.Lock()
		stream.response = response
		stream.err = err
		stream.mu.Unlock()

		// Add assistant response to context
		if err == nil {
			p.addMessage(llm.Message{Role: llm.RoleAssistant, Content: response})
		} else {
			p.rollbackUserMessage(message)
		}
	}()

	return stream, nil
}

// SendStreamRich sends a message and returns a ChatStream with structured events
// (text deltas, tool start/end) instead of raw text chunks.
func (p *Process) SendStreamRich(ctx context.Context, message string) (*ChatStream, error) {
	return p.sendStreamRich(ctx, llm.Message{Role: llm.RoleUser, Content: message}, message)
}

// SendStreamRichWithImages is SendStreamRich for a multimodal user turn: the
// text plus one or more image content blocks (vision-capable models read
// them). History keeps the text (rollbackKey), so the turn persists as its
// text with an image placeholder supplied by the caller.
func (p *Process) SendStreamRichWithImages(ctx context.Context, text string, images []llm.ContentBlock) (*ChatStream, error) {
	if len(images) == 0 {
		return p.SendStreamRich(ctx, text)
	}
	blocks := make([]llm.ContentBlock, 0, len(images)+1)
	if text != "" {
		blocks = append(blocks, llm.ContentBlock{Type: llm.BlockText, Text: text})
	}
	blocks = append(blocks, images...)
	// Content=text so history/skill-context/rollback see the text; Blocks
	// (which include the images) take precedence when the request is built.
	return p.sendStreamRich(ctx, llm.Message{Role: llm.RoleUser, Content: text, Blocks: blocks}, text)
}

// sendStreamRich is the shared streaming-turn core: add the user message, run
// the rich stream, and persist the assistant reply (or roll back on error).
func (p *Process) sendStreamRich(ctx context.Context, userMsg llm.Message, rollbackKey string) (*ChatStream, error) {
	p.mu.Lock()
	if p.status != StatusRunning && p.status != StatusPending {
		p.mu.Unlock()
		return nil, ErrProcessNotRunning
	}
	p.status = StatusRunning
	p.iteration++
	p.metrics.LastActiveAt = time.Now()
	p.mu.Unlock()

	stream := newChatStream()

	go func() {
		p.sendMu.Lock()
		defer p.sendMu.Unlock()

		ctx, cancel := p.withProcessCancel(ctx)
		defer cancel()
		defer close(stream.events)
		defer close(stream.done)

		p.addMessage(userMsg)

		response, err := p.executeLLMStreamRich(ctx, rollbackKey, stream.events)
		stream.mu.Lock()
		stream.response = response
		stream.err = err
		stream.mu.Unlock()

		if err == nil {
			p.addMessage(llm.Message{Role: llm.RoleAssistant, Content: response})
		} else {
			p.rollbackUserMessage(rollbackKey)
		}
	}()

	return stream, nil
}

// Stop terminates the process.
// This is equivalent to killing the process - linked processes will be notified.
func (p *Process) Stop() {
	p.mu.Lock()
	if p.status == StatusCompleted || p.status == StatusFailed {
		p.mu.Unlock()
		return // Already dead
	}

	if p.cancel != nil {
		p.cancel()
	}
	p.status = StatusCompleted
	p.metrics.CompletedAt = time.Now()
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}
	p.mu.Unlock()

	// Propagate exit to linked/monitoring processes
	signal := ExitSignal{
		ProcessID: p.ID,
		AgentName: agentName,
		Reason:    ExitKilled,
		Timestamp: time.Now(),
	}
	p.propagateExit(signal)

	// Notify orchestrator (for name unregistration)
	if p.orchestrator != nil {
		p.orchestrator.emitComplete(p, "")
	}
}

// Complete marks the process as successfully completed with a result.
// This triggers OnProcessComplete callbacks and notifies linked/monitoring processes.
// Normal completion does NOT cause linked processes to die.
func (p *Process) Complete(result string) {
	p.mu.Lock()
	if p.status == StatusCompleted || p.status == StatusFailed {
		p.mu.Unlock()
		return // Already finished
	}

	if p.cancel != nil {
		p.cancel()
	}
	p.status = StatusCompleted
	p.finalResult = result
	p.metrics.CompletedAt = time.Now()
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}
	p.mu.Unlock()

	// Propagate exit to linked/monitoring processes (normal exit)
	signal := ExitSignal{
		ProcessID: p.ID,
		AgentName: agentName,
		Reason:    ExitNormal,
		Result:    result,
		Timestamp: time.Now(),
	}
	p.propagateExit(signal)

	// Notify orchestrator
	if p.orchestrator != nil {
		p.orchestrator.emitComplete(p, result)
	}
}

// Fail marks the process as failed with an error.
// This triggers OnProcessFailed callbacks and notifies linked/monitoring processes.
// Failed processes cause linked processes to die too (unless they trap exits).
func (p *Process) Fail(err error) {
	p.mu.Lock()
	if p.status == StatusCompleted || p.status == StatusFailed {
		p.mu.Unlock()
		return // Already finished
	}

	if p.cancel != nil {
		p.cancel()
	}
	p.status = StatusFailed
	p.metrics.CompletedAt = time.Now()
	p.metrics.Errors++
	agentName := ""
	if p.Agent != nil {
		agentName = p.Agent.Name
	}
	p.mu.Unlock()

	// Propagate exit to linked/monitoring processes (error exit)
	signal := ExitSignal{
		ProcessID: p.ID,
		AgentName: agentName,
		Reason:    ExitError,
		Error:     err,
		Timestamp: time.Now(),
	}
	p.propagateExit(signal)

	// Notify orchestrator
	if p.orchestrator != nil {
		p.orchestrator.emitFailed(p, err)
	}
}

// Messages returns a copy of the conversation history.
func (p *Process) Messages() []llm.Message {
	p.mu.RLock()
	defer p.mu.RUnlock()
	msgs := make([]llm.Message, len(p.messages))
	copy(msgs, p.messages)
	return msgs
}

// Result returns the final result if the process completed.
func (p *Process) Result() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.finalResult
}

// HydrateMessages loads historical messages into a process that has no
// conversation history yet (e.g. after a restart). This is a no-op if
// the process already has messages.
func (p *Process) HydrateMessages(msgs []llm.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.messages) > 0 {
		return // already has conversation history
	}
	p.messages = append(p.messages, msgs...)
}

// MaxResidentMessages caps the number of conversation messages kept in
// memory per process. Long-running agents accumulate every turn forever;
// without a bound, a busy orchestrator's memory grows unboundedly. When
// the cap is exceeded, oldest messages are trimmed but only at user-
// message boundaries — tool_use messages must stay paired with their
// tool_result responses or the Anthropic API rejects the request.
//
// 100 messages comfortably covers ~25-50 turns of agent activity at
// typical density. Apps that genuinely need more set Agent.Context
// (which has its own token-aware bounding via Context.Messages).
const MaxResidentMessages = 100

// addMessage adds a message to the conversation history.
func (p *Process) addMessage(msg llm.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Agent.Context != nil {
		p.Agent.Context.Add(msg)
	}
	p.messages = append(p.messages, msg)

	// Trim oldest messages once we exceed the cap. Walk forward from
	// the front looking for a user message to start the kept window —
	// trimming mid-sequence (e.g. cutting a tool_use without its
	// tool_result) breaks the LLM call. If no clean trim point exists,
	// leave the buffer alone; we'll try again on the next append.
	if len(p.messages) > MaxResidentMessages {
		excess := len(p.messages) - MaxResidentMessages
		// Look for the first user message at or after position `excess`
		// — that's the safe new head.
		newHead := -1
		for i := excess; i < len(p.messages); i++ {
			if p.messages[i].Role == llm.RoleUser {
				newHead = i
				break
			}
		}
		if newHead > 0 {
			// Drop the prefix; keep messages[newHead:] as the new history.
			trimmed := make([]llm.Message, len(p.messages)-newHead)
			copy(trimmed, p.messages[newHead:])
			p.messages = trimmed
		}
	}
}

// rollbackUserMessage removes the trailing user message appended by a Send
// variant whose LLM turn failed without producing an assistant reply. Callers
// must hold sendMu, which guarantees the trailing message is still the one
// this turn appended. The agent's ContextManager mirror is rolled back too
// when it supports removal (see memory.TokenBudgetContext.RemoveLastIf).
func (p *Process) rollbackUserMessage(content string) {
	p.mu.Lock()
	if n := len(p.messages); n > 0 {
		last := p.messages[n-1]
		if last.Role == llm.RoleUser && last.Content == content {
			p.messages = p.messages[:n-1]
		}
	}
	p.mu.Unlock()

	if p.Agent.Context != nil {
		if r, ok := p.Agent.Context.(interface {
			RemoveLastIf(role llm.Role, content string) bool
		}); ok {
			r.RemoveLastIf(llm.RoleUser, content)
		}
	}
}

// buildMessages builds the message list for LLM call.
func (p *Process) buildMessages() []llm.Message {
	var messages []llm.Message

	// Set skill context if using SkillsPrompt
	if sp, ok := p.Agent.System.(*SkillsPrompt); ok {
		p.mu.RLock()
		if len(p.messages) > 0 {
			// Find the last user message
			for i := len(p.messages) - 1; i >= 0; i-- {
				if p.messages[i].Role == llm.RoleUser {
					sp.SetContext(p.messages[i].Content)
					break
				}
			}
		}
		p.mu.RUnlock()
	}

	// Add system prompt
	if p.Agent.System != nil {
		systemContent := p.Agent.System.Prompt()
		p.mu.RLock()
		extra := p.extraSystem
		p.mu.RUnlock()
		if extra != "" {
			systemContent += "\n\n" + extra
		}
		// Ground the agent in real wall-clock time on every turn. Chat
		// agents are long-lived, reused processes, so a date computed at
		// spawn goes stale — the model then falls back on its training-data
		// sense of "today" and confidently reports the wrong date. Because
		// buildMessages runs on every LLM call, this stays current. Day
		// granularity keeps the cached system block stable within a day.
		systemContent += "\n\nToday's date is " + currentDateLine() + "."
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: systemContent,
		})
	}

	// Add conversation history
	if p.Agent.Context != nil {
		maxTokens := DefaultMaxContextTokens
		if p.Agent.MaxTokens > 0 {
			maxTokens = p.Agent.MaxTokens
		}
		messages = append(messages, p.Agent.Context.Messages(maxTokens)...)
	} else {
		p.mu.RLock()
		messages = append(messages, p.messages...)
		p.mu.RUnlock()
	}

	// Filter out messages with no content at all to prevent API errors.
	// Messages carrying typed Blocks are kept regardless of Content.
	filtered := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if len(msg.Blocks) > 0 || strings.TrimSpace(msg.Content) != "" {
			filtered = append(filtered, msg)
		}
	}

	return filtered
}

// currentDateLine returns the current date for the system prompt, e.g.
// "Monday, July 21, 2026 (CDT)". The timezone comes from VEGA_TIMEZONE (an
// IANA name the customer sets when deploying their agent) and falls back to
// UTC when unset or invalid — so tenants in non-UTC zones don't get an
// off-by-one date near midnight. Day granularity is deliberate: the whole
// system prompt is prompt-cached as one block, and a per-turn changing time
// would add nothing but cache churn.
func currentDateLine() string {
	loc := time.UTC
	if tz := os.Getenv("VEGA_TIMEZONE"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	now := time.Now().In(loc)
	return now.Format("Monday, January 2, 2006") + " (" + now.Format("MST") + ")"
}
