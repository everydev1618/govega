package vega

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/everydev1618/govega/llm"
)

// executeLLMLoop runs the LLM call loop, handling tool calls.
func (p *Process) executeLLMLoop(ctx context.Context, message string) (string, CallMetrics, error) {
	metrics := CallMetrics{}

	// Build messages for LLM
	messages := p.buildMessages()

	// Get tools schema if agent has tools
	var toolSchemas []llm.ToolSchema
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

		// Build assistant message with text + tool_use blocks so the API
		// sees proper tool invocations on the next iteration.
		assistantContent := resp.Content
		for _, tc := range resp.ToolCalls {
			assistantContent += "\n" + formatToolCall(tc.ID, tc.Name, tc.Arguments)
		}
		if strings.TrimSpace(assistantContent) != "" {
			messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: assistantContent})
		}

		// Create context with process for tool execution
		toolCtx := ContextWithProcess(ctx, p)

		// Collect all tool results into a single user message.
		var toolResults strings.Builder
		for _, tc := range resp.ToolCalls {
			metrics.ToolCalls = append(metrics.ToolCalls, tc.Name)

			result, err := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
			if err != nil {
				result = "Error: " + err.Error()
			}

			toolResults.WriteString(formatToolResult(tc.ID, tc.Name, result))
			toolResults.WriteString("\n")
		}
		if toolResults.Len() > 0 {
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: strings.TrimSpace(toolResults.String()),
			})
		}
	}

	return "", metrics, ErrMaxIterationsExceeded
}

// executeLLMStream runs streaming LLM call with tool execution loop.
func (p *Process) executeLLMStream(ctx context.Context, message string, chunks chan<- string) (string, error) {
	messages := p.buildMessages()

	var toolSchemas []llm.ToolSchema
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
		var toolCalls []llm.ToolCall
		var currentToolCall *llm.ToolCall
		var currentToolJSON string

		for event := range eventCh {
			if event.Error != nil {
				return fullResponse, event.Error
			}

			switch event.Type {
			case llm.StreamEventContentDelta:
				if event.Delta != "" {
					chunks <- event.Delta
					iterResponse += event.Delta
					fullResponse += event.Delta
				}
			case llm.StreamEventToolStart:
				if event.ToolCall != nil {
					currentToolCall = &llm.ToolCall{
						ID:        event.ToolCall.ID,
						Name:      event.ToolCall.Name,
						Arguments: make(map[string]any),
					}
					currentToolJSON = ""
				}
			case llm.StreamEventToolDelta:
				if currentToolCall != nil {
					currentToolJSON += event.Delta
				}
			case llm.StreamEventContentEnd:
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

		// Build assistant message with text + tool_use blocks.
		assistantContent := iterResponse
		for _, tc := range toolCalls {
			assistantContent += "\n" + formatToolCall(tc.ID, tc.Name, tc.Arguments)
		}
		if strings.TrimSpace(assistantContent) != "" {
			messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: assistantContent})
		}

		// Create context with process for tool execution
		toolCtx := ContextWithProcess(ctx, p)

		// Collect all tool results into a single user message.
		var toolResults strings.Builder
		for _, tc := range toolCalls {
			p.mu.Lock()
			p.metrics.ToolCalls++
			p.mu.Unlock()

			result, err := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
			if err != nil {
				result = "Error: " + err.Error()
			}

			toolResults.WriteString(formatToolResult(tc.ID, tc.Name, result))
			toolResults.WriteString("\n")
		}
		if toolResults.Len() > 0 {
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: strings.TrimSpace(toolResults.String()),
			})
		}
	}

	return fullResponse, ErrMaxIterationsExceeded
}

// executeLLMStreamRich runs a streaming LLM call loop, emitting structured
// ChatEvent values (text deltas + tool lifecycle) instead of raw string chunks.
func (p *Process) executeLLMStreamRich(ctx context.Context, message string, events chan<- ChatEvent) (string, error) {
	messages := p.buildMessages()

	var toolSchemas []llm.ToolSchema
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

		var iterResponse string
		var toolCalls []llm.ToolCall
		var currentToolCall *llm.ToolCall
		var currentToolJSON string

		for ev := range eventCh {
			if ev.Error != nil {
				return fullResponse, ev.Error
			}

			switch ev.Type {
			case llm.StreamEventContentDelta:
				if ev.Delta != "" {
					events <- ChatEvent{Type: ChatEventTextDelta, Delta: ev.Delta}
					iterResponse += ev.Delta
					fullResponse += ev.Delta
				}
			case llm.StreamEventToolStart:
				if ev.ToolCall != nil {
					currentToolCall = &llm.ToolCall{
						ID:        ev.ToolCall.ID,
						Name:      ev.ToolCall.Name,
						Arguments: make(map[string]any),
					}
					currentToolJSON = ""
				}
			case llm.StreamEventToolDelta:
				if currentToolCall != nil {
					currentToolJSON += ev.Delta
				}
			case llm.StreamEventContentEnd:
				if currentToolCall != nil {
					if currentToolJSON != "" {
						json.Unmarshal([]byte(currentToolJSON), &currentToolCall.Arguments)
					}
					// Emit tool_start with complete arguments.
					events <- ChatEvent{
						Type:       ChatEventToolStart,
						ToolCallID: currentToolCall.ID,
						ToolName:   currentToolCall.Name,
						Arguments:  currentToolCall.Arguments,
					}
					toolCalls = append(toolCalls, *currentToolCall)
					currentToolCall = nil
					currentToolJSON = ""
				}
			}
		}

		if len(toolCalls) == 0 {
			return fullResponse, nil
		}

		// Build assistant message with text + tool_use blocks.
		assistantContent := iterResponse
		for _, tc := range toolCalls {
			assistantContent += "\n" + formatToolCall(tc.ID, tc.Name, tc.Arguments)
		}
		if strings.TrimSpace(assistantContent) != "" {
			messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: assistantContent})
		}

		toolCtx := ContextWithProcess(ctx, p)

		// Execute tools and collect results into a single user message.
		var toolResults strings.Builder
		for _, tc := range toolCalls {
			p.mu.Lock()
			p.metrics.ToolCalls++
			p.mu.Unlock()

			start := time.Now()
			result, execErr := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
			elapsed := toolDuration(start)
			if execErr != nil {
				result = "Error: " + execErr.Error()
			}

			events <- ChatEvent{
				Type:       ChatEventToolEnd,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Result:     result,
				DurationMs: elapsed,
			}

			toolResults.WriteString(formatToolResult(tc.ID, tc.Name, result))
			toolResults.WriteString("\n")
		}
		if toolResults.Len() > 0 {
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: strings.TrimSpace(toolResults.String()),
			})
		}
	}

	return fullResponse, ErrMaxIterationsExceeded
}

// callLLMWithRetry calls the LLM with retry logic based on agent's RetryPolicy.
func (p *Process) callLLMWithRetry(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
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
