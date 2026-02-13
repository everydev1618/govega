package vega

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// contextKey is a type for context keys used by vega.
type contextKey string

// processContextKey is the context key for the current process.
const processContextKey contextKey = "vega.process"

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
	messages []Message

	// iteration count
	iteration int

	// llm is the backend to use
	llm LLM

	// orchestrator reference for child spawning
	orchestrator *Orchestrator

	// mutex for thread safety
	mu sync.RWMutex

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

	// Automatic restart support
	restartPolicy ChildRestart
	spawnOpts     []SpawnOption

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
	Iterations   int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	StartedAt    time.Time
	CompletedAt  time.Time
	LastActiveAt time.Time
	ToolCalls    int
	Errors       int
}

// SendResult is the result of a Send operation.
type SendResult struct {
	Response string
	Error    error
	Metrics  CallMetrics
}

// CallMetrics tracks a single LLM call.
type CallMetrics struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	LatencyMs    int64
	ToolCalls    []string
	Retries      int
}

// Status returns the current process status.
func (p *Process) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
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

// Send sends a message and waits for a response.
func (p *Process) Send(ctx context.Context, message string) (string, error) {
	p.mu.Lock()
	if p.status != StatusRunning && p.status != StatusPending {
		p.mu.Unlock()
		return "", ErrProcessNotRunning
	}
	p.status = StatusRunning
	p.iteration++
	p.metrics.LastActiveAt = time.Now()
	p.mu.Unlock()

	// Add user message to context
	p.addMessage(Message{Role: RoleUser, Content: message})

	// Execute the LLM call loop (may involve tool calls)
	response, callMetrics, err := p.executeLLMLoop(ctx, message)
	if err != nil {
		p.mu.Lock()
		p.metrics.Errors++
		p.mu.Unlock()
		return "", err
	}

	// Update metrics
	p.mu.Lock()
	p.metrics.InputTokens += callMetrics.InputTokens
	p.metrics.OutputTokens += callMetrics.OutputTokens
	p.metrics.CostUSD += callMetrics.CostUSD
	p.metrics.ToolCalls += len(callMetrics.ToolCalls)
	p.mu.Unlock()

	// Add assistant response to context
	p.addMessage(Message{Role: RoleAssistant, Content: response})

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

	// Add user message to context
	p.addMessage(Message{Role: RoleUser, Content: message})

	// Create stream
	stream := &Stream{
		chunks: make(chan string, DefaultStreamBufferSize),
		done:   make(chan struct{}),
	}

	// Execute streaming in goroutine
	go func() {
		defer close(stream.chunks)
		defer close(stream.done)

		response, err := p.executeLLMStream(ctx, message, stream.chunks)
		stream.mu.Lock()
		stream.response = response
		stream.err = err
		stream.mu.Unlock()

		// Add assistant response to context
		if err == nil {
			p.addMessage(Message{Role: RoleAssistant, Content: response})
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
func (p *Process) Messages() []Message {
	p.mu.RLock()
	defer p.mu.RUnlock()
	msgs := make([]Message, len(p.messages))
	copy(msgs, p.messages)
	return msgs
}

// Result returns the final result if the process completed.
func (p *Process) Result() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.finalResult
}

// addMessage adds a message to the conversation history.
func (p *Process) addMessage(msg Message) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Agent.Context != nil {
		p.Agent.Context.Add(msg)
	}
	p.messages = append(p.messages, msg)
}

// buildMessages builds the message list for LLM call.
func (p *Process) buildMessages() []Message {
	var messages []Message

	// Set skill context if using SkillsPrompt
	if sp, ok := p.Agent.System.(*SkillsPrompt); ok {
		p.mu.RLock()
		if len(p.messages) > 0 {
			// Find the last user message
			for i := len(p.messages) - 1; i >= 0; i-- {
				if p.messages[i].Role == RoleUser {
					sp.SetContext(p.messages[i].Content)
					break
				}
			}
		}
		p.mu.RUnlock()
	}

	// Add system prompt
	if p.Agent.System != nil {
		messages = append(messages, Message{
			Role:    RoleSystem,
			Content: p.Agent.System.Prompt(),
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

	// Filter out any messages with empty content to prevent API errors
	filtered := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.Content) != "" {
			filtered = append(filtered, msg)
		}
	}

	return filtered
}

// formatToolResult formats a tool result for the LLM.
func formatToolResult(id, name, result string) string {
	return "<tool_result tool_use_id=\"" + id + "\" name=\"" + name + "\">\n" + result + "\n</tool_result>"
}

// formatToolCall formats a tool call for the assistant message.
func formatToolCall(id, name string, args map[string]any) string {
	argsJSON, _ := json.Marshal(args)
	return "<tool_use id=\"" + id + "\" name=\"" + name + "\">\n" + string(argsJSON) + "\n</tool_use>"
}
