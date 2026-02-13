package vega

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"strings"
	"time"
)

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
	maxIterations := DefaultMaxIterations
	if p.Agent.MaxIterations > 0 {
		maxIterations = p.Agent.MaxIterations
	}
	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return "", metrics, ctx.Err()
		default:
		}

		// Call LLM with retry support
		resp, err := p.callLLMWithRetry(ctx, messages, toolSchemas)
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

		// Execute tool calls - add assistant message only if it has content
		if strings.TrimSpace(resp.Content) != "" {
			messages = append(messages, Message{Role: RoleAssistant, Content: resp.Content})
		}

		// Create context with process for tool execution
		toolCtx := ContextWithProcess(ctx, p)

		for _, tc := range resp.ToolCalls {
			metrics.ToolCalls = append(metrics.ToolCalls, tc.Name)

			result, err := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
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
	maxIterations := DefaultMaxIterations
	if p.Agent.MaxIterations > 0 {
		maxIterations = p.Agent.MaxIterations
	}

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
		// Only add assistant message if it has content
		if strings.TrimSpace(assistantContent) != "" {
			messages = append(messages, Message{Role: RoleAssistant, Content: assistantContent})
		}

		// Create context with process for tool execution
		toolCtx := ContextWithProcess(ctx, p)

		// Execute tool calls and add results
		for _, tc := range toolCalls {
			p.mu.Lock()
			p.metrics.ToolCalls++
			p.mu.Unlock()

			result, err := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
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

// callLLMWithRetry calls the LLM with retry logic based on agent's RetryPolicy.
func (p *Process) callLLMWithRetry(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error) {
	policy := p.Agent.Retry
	maxAttempts := 1
	if policy != nil && policy.MaxAttempts > 0 {
		maxAttempts = policy.MaxAttempts
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		start := time.Now()
		resp, err := p.llm.Generate(ctx, messages, tools)
		latency := time.Since(start)

		if err == nil {
			slog.Debug("llm call succeeded",
				"process_id", p.ID,
				"agent", p.Agent.Name,
				"attempt", attempt+1,
				"latency_ms", latency.Milliseconds(),
				"input_tokens", resp.InputTokens,
				"output_tokens", resp.OutputTokens,
			)
			return resp, nil
		}

		lastErr = err
		errClass := ClassifyError(err)

		slog.Warn("llm call failed",
			"process_id", p.ID,
			"agent", p.Agent.Name,
			"attempt", attempt+1,
			"max_attempts", maxAttempts,
			"error", err.Error(),
			"error_class", errClass,
			"latency_ms", latency.Milliseconds(),
		)

		// Check if we should retry
		if !ShouldRetry(err, policy, attempt) {
			slog.Debug("not retrying",
				"process_id", p.ID,
				"reason", "retry policy",
			)
			return nil, err
		}

		// Calculate backoff delay
		delay := p.calculateRetryDelay(policy, attempt)
		if delay > 0 {
			slog.Debug("retrying after backoff",
				"process_id", p.ID,
				"delay_ms", delay.Milliseconds(),
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		// Update metrics
		p.mu.Lock()
		p.metrics.Errors++
		p.mu.Unlock()
	}

	return nil, lastErr
}

// calculateRetryDelay computes the delay before the next retry attempt.
func (p *Process) calculateRetryDelay(policy *RetryPolicy, attempt int) time.Duration {
	if policy == nil || policy.Backoff.Initial == 0 {
		return 0
	}

	var delay time.Duration
	switch policy.Backoff.Type {
	case BackoffExponential:
		multiplier := policy.Backoff.Multiplier
		if multiplier == 0 {
			multiplier = 2.0
		}
		delay = time.Duration(float64(policy.Backoff.Initial) * pow64(multiplier, float64(attempt)))
	case BackoffLinear:
		delay = policy.Backoff.Initial * time.Duration(attempt+1)
	case BackoffConstant:
		delay = policy.Backoff.Initial
	default:
		delay = policy.Backoff.Initial
	}

	// Apply max limit
	if policy.Backoff.Max > 0 && delay > policy.Backoff.Max {
		delay = policy.Backoff.Max
	}

	// Apply jitter if configured
	if policy.Backoff.Jitter > 0 {
		jitterRange := float64(delay) * policy.Backoff.Jitter
		jitter := (rand.Float64()*2 - 1) * jitterRange // -jitter to +jitter
		delay = time.Duration(float64(delay) + jitter)
		if delay < 0 {
			delay = 0
		}
	}

	return delay
}

// pow64 is a simple power function for floats.
func pow64(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}
