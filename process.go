package vega

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

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

	// result channel for async operations
	resultCh chan *SendResult

	// finalResult stores the result when process completes
	finalResult string
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
		chunks: make(chan string, 100),
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
func (p *Process) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancel != nil {
		p.cancel()
	}
	p.status = StatusCompleted
	p.metrics.CompletedAt = time.Now()
}

// Complete marks the process as successfully completed with a result.
// This triggers OnProcessComplete callbacks.
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
	p.mu.Unlock()

	// Notify orchestrator
	if p.orchestrator != nil {
		p.orchestrator.emitComplete(p, result)
	}
}

// Fail marks the process as failed with an error.
// This triggers OnProcessFailed callbacks.
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
	p.mu.Unlock()

	// Notify orchestrator
	if p.orchestrator != nil {
		p.orchestrator.emitFailed(p, err)
	}
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

// executeLLMLoop runs the LLM call loop, handling tool calls.
func (p *Process) executeLLMLoop(ctx context.Context, message string) (string, CallMetrics, error) {
	metrics := CallMetrics{}

	// Build messages for LLM
	messages := p.buildMessages()

	// Get tools schema if agent has tools
	var toolSchemas []ToolSchema
	if p.Agent.Tools != nil {
		toolSchemas = p.Agent.Tools.Schema()
	}

	// Main loop - keep calling LLM until we get a final response (no tool calls)
	maxIterations := 50 // Safety limit
	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return "", metrics, ctx.Err()
		default:
		}

		// Call LLM
		resp, err := p.llm.Generate(ctx, messages, toolSchemas)
		if err != nil {
			return "", metrics, err
		}

		// Update metrics
		metrics.InputTokens += resp.InputTokens
		metrics.OutputTokens += resp.OutputTokens
		metrics.CostUSD += resp.CostUSD
		metrics.LatencyMs += resp.LatencyMs

		// If no tool calls, we're done
		if len(resp.ToolCalls) == 0 {
			return resp.Content, metrics, nil
		}

		// Execute tool calls
		messages = append(messages, Message{Role: RoleAssistant, Content: resp.Content})

		for _, tc := range resp.ToolCalls {
			metrics.ToolCalls = append(metrics.ToolCalls, tc.Name)

			result, err := p.Agent.Tools.Execute(ctx, tc.Name, tc.Arguments)
			if err != nil {
				result = "Error: " + err.Error()
			}

			// Add tool result as user message (this is how Anthropic expects it)
			messages = append(messages, Message{
				Role:    RoleUser,
				Content: formatToolResult(tc.ID, tc.Name, result),
			})
		}
	}

	return "", metrics, ErrMaxIterationsExceeded
}

// executeLLMStream runs streaming LLM call with tool execution loop.
func (p *Process) executeLLMStream(ctx context.Context, message string, chunks chan<- string) (string, error) {
	messages := p.buildMessages()

	var toolSchemas []ToolSchema
	if p.Agent.Tools != nil {
		toolSchemas = p.Agent.Tools.Schema()
	}

	var fullResponse string
	maxIterations := 50 // Safety limit

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return fullResponse, ctx.Err()
		default:
		}

		eventCh, err := p.llm.GenerateStream(ctx, messages, toolSchemas)
		if err != nil {
			return fullResponse, err
		}

		// Collect response and tool calls from this iteration
		var iterResponse string
		var toolCalls []ToolCall
		var currentToolCall *ToolCall
		var currentToolJSON string

		for event := range eventCh {
			if event.Error != nil {
				return fullResponse, event.Error
			}

			switch event.Type {
			case StreamEventContentDelta:
				if event.Delta != "" {
					chunks <- event.Delta
					iterResponse += event.Delta
					fullResponse += event.Delta
				}
			case StreamEventToolStart:
				if event.ToolCall != nil {
					currentToolCall = &ToolCall{
						ID:        event.ToolCall.ID,
						Name:      event.ToolCall.Name,
						Arguments: make(map[string]any),
					}
					currentToolJSON = ""
				}
			case StreamEventToolDelta:
				if currentToolCall != nil {
					currentToolJSON += event.Delta
				}
			case StreamEventContentEnd:
				// If we were building a tool call, finalize it
				if currentToolCall != nil {
					if currentToolJSON != "" {
						json.Unmarshal([]byte(currentToolJSON), &currentToolCall.Arguments)
					}
					toolCalls = append(toolCalls, *currentToolCall)
					currentToolCall = nil
					currentToolJSON = ""
				}
			}
		}

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			return fullResponse, nil
		}

		// Add assistant message with the response (include tool call info if no text)
		assistantContent := iterResponse
		if assistantContent == "" {
			// Build a representation of the tool calls for the message
			var toolParts []string
			for _, tc := range toolCalls {
				toolParts = append(toolParts, formatToolCall(tc.ID, tc.Name, tc.Arguments))
			}
			assistantContent = strings.Join(toolParts, "\n")
		}
		messages = append(messages, Message{Role: RoleAssistant, Content: assistantContent})

		// Execute tool calls and add results
		for _, tc := range toolCalls {
			p.mu.Lock()
			p.metrics.ToolCalls++
			p.mu.Unlock()

			result, err := p.Agent.Tools.Execute(ctx, tc.Name, tc.Arguments)
			if err != nil {
				result = "Error: " + err.Error()
			}

			// Add tool result as user message
			messages = append(messages, Message{
				Role:    RoleUser,
				Content: formatToolResult(tc.ID, tc.Name, result),
			})
		}
	}

	return fullResponse, ErrMaxIterationsExceeded
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
		maxTokens := 100000 // Default, could be configurable
		if p.Agent.MaxTokens > 0 {
			maxTokens = p.Agent.MaxTokens
		}
		messages = append(messages, p.Agent.Context.Messages(maxTokens)...)
	} else {
		p.mu.RLock()
		messages = append(messages, p.messages...)
		p.mu.RUnlock()
	}

	return messages
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
// Future represents an asynchronous operation result.
type Future struct {
	result    string
	err       error
	completed bool
	done      chan struct{}
	cancel    chan struct{}
	mu        sync.RWMutex
}

// Await waits for the future to complete and returns the result.
func (f *Future) Await(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-f.done:
		f.mu.RLock()
		defer f.mu.RUnlock()
		return f.result, f.err
	}
}

// Done returns true if the future has completed.
func (f *Future) Done() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.completed
}

// Result returns the result if completed, or error if not.
func (f *Future) Result() (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.completed {
		return "", ErrNotCompleted
	}
	return f.result, f.err
}

// Cancel cancels the future.
func (f *Future) Cancel() {
	select {
	case f.cancel <- struct{}{}:
	default:
	}
}

// Stream represents a streaming response.
type Stream struct {
	chunks   chan string
	response string
	err      error
	done     chan struct{}
	mu       sync.RWMutex
}

// Chunks returns the channel of response chunks.
func (s *Stream) Chunks() <-chan string {
	return s.chunks
}

// Response returns the complete response after streaming is done.
func (s *Stream) Response() string {
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.response
}

// Err returns any error that occurred during streaming.
func (s *Stream) Err() error {
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}
