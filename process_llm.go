package vega

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/everydev1618/govega/internal/v39a"
	"github.com/everydev1618/govega/llm"
)

// v39aReporter is the lazily-initialised reporter used by callLLMWithRetry
// to ship per-call cost telemetry to v39a's control panel. Nil means env
// vars weren't set (telemetry disabled) — no calls fire.
var (
	v39aReporterOnce sync.Once
	v39aReporter     *v39a.Reporter
)

func getV39AReporter() *v39a.Reporter {
	v39aReporterOnce.Do(func() {
		v39aReporter = v39a.NewReporterFromEnv()
	})
	return v39aReporter
}

// reportLLMCost fires a cost_recorded event for a successful LLM call,
// async + fire-and-forget. The reporter is nil when V39A_REPORTER_URL /
// V39A_INSTANCE_TOKEN aren't set; in that case this is a no-op.
//
// The conversation_id is the Process ID — v39a auto-creates a
// conversation row when it sees a cost for an unknown id, so no
// separate conversation_started event is needed for the slim slice.
// Vendor is "anthropic" for any claude-* model, "openai" otherwise.
func reportLLMCost(p *Process, resp *llm.LLMResponse, stepType, model string) {
	r := getV39AReporter()
	if r == nil || resp == nil || resp.CostUSD <= 0 {
		return
	}
	vendor := "openai"
	if strings.HasPrefix(model, "claude-") {
		vendor = "anthropic"
	}
	ev := v39a.CostEvent{
		ConversationID: p.ID,
		Kind:           "llm",
		Vendor:         vendor,
		Units:          1,
		UnitCostUSD:    resp.CostUSD,
		StepType:       stepType,
		Model:          model,
	}
	go func() {
		if err := r.RecordCost(context.Background(), ev); err != nil {
			slog.Warn("v39a reporter: cost_recorded failed",
				"process_id", p.ID,
				"agent", p.Agent.Name,
				"error", err.Error(),
			)
		}
	}()
}

// llmCallContext returns ctx enriched with Agent-level overrides for
// the LLM backend to read. The model is chosen by Agent.ModelFor(role) —
// agents without a per-step Models map get Agent.Model.
func (p *Process) llmCallContext(ctx context.Context, role string) context.Context {
	return llm.ContextWithOptions(ctx, llm.Options{
		Model:       p.Agent.ModelFor(role),
		Temperature: p.Agent.Temperature,
		MaxTokens:   p.Agent.MaxTokens,
		Effort:      p.Agent.Effort,
	})
}

// stepTypeFor derives the step-type tag for the upcoming LLM call from
// the agent's current tool surface and the conversation so far.
func (p *Process) stepTypeFor(messages []llm.Message) string {
	var toolNames []string
	if p.Agent.Tools != nil {
		for _, sch := range p.Agent.Tools.Schema() {
			toolNames = append(toolNames, sch.Name)
		}
	}
	return deriveStepType(toolNames, countUserMessages(messages))
}

// codeShapedTools are tool names that strongly suggest the agent is
// about to produce or run code on this turn. The list is conservative —
// false positives push a turn onto a heavier model than needed, which
// is a cost regression, not a correctness one.
var codeShapedTools = map[string]struct{}{
	"write_file":  {},
	"edit_file":   {},
	"run_command": {},
	"shell":       {},
	"bash":        {},
	"apply_patch": {},
}

// deriveStepType returns a best-effort tag describing the upcoming LLM
// turn, given the agent's tool surface and how much of a conversation
// has accrued. Returns "" when no rule fires so ModelFor falls back to
// the agent's primary Model. Pure so it's trivially testable.
func deriveStepType(toolNames []string, userMessages int) string {
	for _, n := range toolNames {
		if _, ok := codeShapedTools[n]; ok {
			return "code"
		}
	}
	if len(toolNames) == 0 {
		return "chat"
	}
	if userMessages <= 1 {
		return "classify"
	}
	return ""
}

// countUserMessages counts user-role messages in a conversation.
func countUserMessages(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == llm.RoleUser {
			n++
		}
	}
	return n
}

// sendChunk sends s on ch, aborting if ctx is cancelled. Returns false if the
// send was abandoned — prevents the producer goroutine from blocking forever
// when the stream consumer (e.g. a disconnected SSE client) stops reading.
func sendChunk(ctx context.Context, ch chan<- string, s string) bool {
	select {
	case ch <- s:
		return true
	case <-ctx.Done():
		return false
	}
}

// sendEvent sends ev on ch, aborting if ctx is cancelled. See sendChunk.
func sendEvent(ctx context.Context, ch chan<- ChatEvent, ev ChatEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// executeLLMLoop runs the LLM call loop, handling tool calls.
// blockCollector assembles ordered typed content blocks from stream events
// so the assistant turn can be replayed to the API with thinking blocks
// (text + signature) and tool_use blocks intact. handle returns the
// finalized tool call when a tool_use block closes, nil otherwise.
type blockCollector struct {
	blocks    []llm.ContentBlock
	toolCalls []llm.ToolCall
	open      string // type of the currently open block, "" when none
	toolJSON  string
}

func (c *blockCollector) handle(ev llm.StreamEvent) *llm.ToolCall {
	switch ev.Type {
	case llm.StreamEventThinkingStart:
		c.blocks = append(c.blocks, llm.ContentBlock{Type: llm.BlockThinking})
		c.open = llm.BlockThinking
	case llm.StreamEventThinkingDelta:
		if c.open == llm.BlockThinking {
			c.blocks[len(c.blocks)-1].Text += ev.Delta
		}
	case llm.StreamEventThinkingSignature:
		if c.open == llm.BlockThinking {
			c.blocks[len(c.blocks)-1].Signature += ev.Delta
		}
	case llm.StreamEventContentStart:
		c.blocks = append(c.blocks, llm.ContentBlock{Type: llm.BlockText})
		c.open = llm.BlockText
	case llm.StreamEventContentDelta:
		// Some backends emit deltas without a content_start.
		if c.open != llm.BlockText {
			c.blocks = append(c.blocks, llm.ContentBlock{Type: llm.BlockText})
			c.open = llm.BlockText
		}
		c.blocks[len(c.blocks)-1].Text += ev.Delta
	case llm.StreamEventToolStart:
		if ev.ToolCall != nil {
			c.blocks = append(c.blocks, llm.ContentBlock{
				Type: llm.BlockToolUse,
				ID:   ev.ToolCall.ID,
				Name: ev.ToolCall.Name,
			})
			c.open = llm.BlockToolUse
			c.toolJSON = ""
		}
	case llm.StreamEventToolDelta:
		if c.open == llm.BlockToolUse {
			c.toolJSON += ev.Delta
		}
	case llm.StreamEventContentEnd:
		wasTool := c.open == llm.BlockToolUse
		c.open = ""
		if wasTool {
			last := &c.blocks[len(c.blocks)-1]
			args := make(map[string]any)
			if c.toolJSON != "" {
				json.Unmarshal([]byte(c.toolJSON), &args)
			}
			c.toolJSON = ""
			last.Arguments = args
			call := llm.ToolCall{ID: last.ID, Name: last.Name, Arguments: args}
			c.toolCalls = append(c.toolCalls, call)
			return &call
		}
	}
	return nil
}

// assistantTurnMessage builds the assistant message to replay in the tool
// loop. Backends with typed block support supply blocks (thinking, text,
// tool_use); for the rest the turn is synthesized from the flat fields.
func assistantTurnMessage(content string, blocks []llm.ContentBlock, toolCalls []llm.ToolCall) llm.Message {
	if len(blocks) == 0 {
		if strings.TrimSpace(content) != "" {
			blocks = append(blocks, llm.ContentBlock{Type: llm.BlockText, Text: content})
		}
		for _, tc := range toolCalls {
			blocks = append(blocks, llm.ContentBlock{
				Type:      llm.BlockToolUse,
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}
	}
	return llm.Message{Role: llm.RoleAssistant, Content: content, Blocks: blocks}
}

// toolResultsMessage builds the user message carrying typed tool_result
// blocks for the executed calls.
func toolResultsMessage(results []llm.ContentBlock) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: results}
}

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
	maxIterations := p.effectiveMaxIterations()
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
		metrics.CacheCreationInputTokens += resp.CacheCreationInputTokens
		metrics.CacheReadInputTokens += resp.CacheReadInputTokens
		metrics.CostUSD += resp.CostUSD
		metrics.LatencyMs += resp.LatencyMs

		// If no tool calls, we're done
		if len(resp.ToolCalls) == 0 {
			return resp.Content, metrics, nil
		}

		// Replay the assistant turn with typed blocks (thinking, text,
		// tool_use) so the API sees the exact structure on the next
		// iteration — no markup round trip.
		messages = append(messages, assistantTurnMessage(resp.Content, resp.Blocks, resp.ToolCalls))

		// Create context with process for tool execution
		toolCtx := ContextWithProcess(ctx, p)

		// Execute all tool calls in parallel and collect results.
		results := make([]llm.ContentBlock, len(resp.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range resp.ToolCalls {
			metrics.ToolCalls = append(metrics.ToolCalls, tc.Name)
			wg.Add(1)
			go func(idx int, tc llm.ToolCall) {
				defer wg.Done()
				result, err := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
				if err != nil {
					result = "Error: " + err.Error()
				}
				results[idx] = llm.ContentBlock{
					Type:      llm.BlockToolResult,
					ToolUseID: tc.ID,
					Content:   result,
					IsError:   err != nil,
				}
			}(i, tc)
		}
		wg.Wait()

		messages = append(messages, toolResultsMessage(results))
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
	maxIterations := p.effectiveMaxIterations()

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return fullResponse, ctx.Err()
		default:
		}

		if err := p.checkBudget(); err != nil {
			return fullResponse, err
		}

		eventCh, err := p.llm.GenerateStream(p.llmCallContext(ctx, p.stepTypeFor(messages)), messages, toolSchemas)
		if err != nil {
			return fullResponse, err
		}

		// Collect this iteration's typed blocks and tool calls.
		var iterResponse string
		collector := &blockCollector{}

		for event := range eventCh {
			if event.Error != nil {
				return fullResponse, event.Error
			}

			collector.handle(event)

			if event.Type == llm.StreamEventContentDelta && event.Delta != "" {
				if !sendChunk(ctx, chunks, event.Delta) {
					return fullResponse, ctx.Err()
				}
				iterResponse += event.Delta
				fullResponse += event.Delta
			}
		}

		// If no tool calls, we're done
		if len(collector.toolCalls) == 0 {
			return fullResponse, nil
		}

		messages = append(messages, assistantTurnMessage(iterResponse, collector.blocks, collector.toolCalls))

		// Create context with process for tool execution
		toolCtx := ContextWithProcess(ctx, p)

		// Execute all tool calls in parallel and collect results.
		streamResults := make([]llm.ContentBlock, len(collector.toolCalls))
		var wg sync.WaitGroup
		for i, tc := range collector.toolCalls {
			p.mu.Lock()
			p.metrics.ToolCalls++
			p.mu.Unlock()
			wg.Add(1)
			go func(idx int, tc llm.ToolCall) {
				defer wg.Done()
				result, err := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
				if err != nil {
					result = "Error: " + err.Error()
				}
				streamResults[idx] = llm.ContentBlock{
					Type:      llm.BlockToolResult,
					ToolUseID: tc.ID,
					Content:   result,
					IsError:   err != nil,
				}
			}(i, tc)
		}
		wg.Wait()

		messages = append(messages, toolResultsMessage(streamResults))
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
	var totalInputTokens, totalOutputTokens int
	var totalCacheCreationTokens, totalCacheReadTokens int

	// Update process metrics when the function returns.
	defer func() {
		costUSD := llm.CalculateCost(p.Agent.Model, totalInputTokens, totalOutputTokens,
			totalCacheCreationTokens, totalCacheReadTokens)
		p.mu.Lock()
		p.metrics.InputTokens += totalInputTokens
		p.metrics.OutputTokens += totalOutputTokens
		p.metrics.CacheCreationInputTokens += totalCacheCreationTokens
		p.metrics.CacheReadInputTokens += totalCacheReadTokens
		p.metrics.CostUSD += costUSD
		p.mu.Unlock()
	}()

	maxIterations := p.effectiveMaxIterations()

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return fullResponse, ctx.Err()
		default:
		}

		if err := p.checkBudget(); err != nil {
			return fullResponse, err
		}

		eventCh, err := p.llm.GenerateStream(p.llmCallContext(ctx, p.stepTypeFor(messages)), messages, toolSchemas)
		if err != nil {
			return fullResponse, err
		}

		var iterResponse string
		collector := &blockCollector{}

		for ev := range eventCh {
			if ev.Error != nil {
				return fullResponse, ev.Error
			}

			finishedTool := collector.handle(ev)

			switch ev.Type {
			case llm.StreamEventMessageStart:
				totalInputTokens += ev.InputTokens
				totalCacheCreationTokens += ev.CacheCreationInputTokens
				totalCacheReadTokens += ev.CacheReadInputTokens
			case llm.StreamEventMessageEnd:
				totalOutputTokens += ev.OutputTokens
			case llm.StreamEventContentDelta:
				if ev.Delta != "" {
					if !sendEvent(ctx, events, ChatEvent{Type: ChatEventTextDelta, Delta: ev.Delta}) {
						return fullResponse, ctx.Err()
					}
					iterResponse += ev.Delta
					fullResponse += ev.Delta
				}
			case llm.StreamEventContentEnd:
				if finishedTool != nil {
					// Emit tool_start with complete arguments.
					if !sendEvent(ctx, events, ChatEvent{
						Type:       ChatEventToolStart,
						ToolCallID: finishedTool.ID,
						ToolName:   finishedTool.Name,
						Arguments:  finishedTool.Arguments,
					}) {
						return fullResponse, ctx.Err()
					}
				}
			}
		}

		if len(collector.toolCalls) == 0 {
			return fullResponse, nil
		}

		messages = append(messages, assistantTurnMessage(iterResponse, collector.blocks, collector.toolCalls))

		toolCtx := ContextWithProcess(ctx, p)
		toolCtx = ContextWithEventSink(toolCtx, events)

		// Execute all tool calls in parallel and collect results.
		type richToolResult struct {
			block   llm.ContentBlock
			name    string
			elapsed int64
		}
		richResults := make([]richToolResult, len(collector.toolCalls))
		var wg sync.WaitGroup
		for i, tc := range collector.toolCalls {
			p.mu.Lock()
			p.metrics.ToolCalls++
			p.mu.Unlock()
			wg.Add(1)
			go func(idx int, tc llm.ToolCall) {
				defer wg.Done()
				start := time.Now()
				result, execErr := p.Agent.Tools.Execute(toolCtx, tc.Name, tc.Arguments)
				elapsed := toolDuration(start)
				if execErr != nil {
					result = "Error: " + execErr.Error()
				}
				richResults[idx] = richToolResult{
					block: llm.ContentBlock{
						Type:      llm.BlockToolResult,
						ToolUseID: tc.ID,
						Content:   result,
						IsError:   execErr != nil,
					},
					name:    tc.Name,
					elapsed: elapsed,
				}
			}(i, tc)
		}
		wg.Wait()

		// Emit tool end events and build the result message in order.
		resultBlocks := make([]llm.ContentBlock, 0, len(richResults))
		for _, tr := range richResults {
			if !sendEvent(ctx, events, ChatEvent{
				Type:       ChatEventToolEnd,
				ToolCallID: tr.block.ToolUseID,
				ToolName:   tr.name,
				Result:     tr.block.Content,
				DurationMs: tr.elapsed,
			}) {
				return fullResponse, ctx.Err()
			}
			resultBlocks = append(resultBlocks, tr.block)
		}
		messages = append(messages, toolResultsMessage(resultBlocks))
		// Separate tool results from the next LLM response with a newline
		// so streamed text doesn't concatenate without whitespace.
		if !sendEvent(ctx, events, ChatEvent{Type: ChatEventTextDelta, Delta: "\n\n"}) {
			return fullResponse, ctx.Err()
		}
		fullResponse += "\n\n"
	}

	return fullResponse, ErrMaxIterationsExceeded
}

// callLLMWithRetry calls the LLM with retry logic based on agent's RetryPolicy.
// It also enforces per-agent rate limits and circuit breaker state.
// checkBudget enforces the agent's cost limit before another LLM call.
// It returns ErrBudgetExceeded (BudgetBlock) once accumulated cost reaches the
// limit, logs and allows (BudgetWarn), or is a no-op (BudgetAllow / no budget).
func (p *Process) checkBudget() error {
	b := p.Agent.Budget
	if b == nil || b.Limit <= 0 {
		return nil
	}
	spent := p.Metrics().CostUSD
	if spent < b.Limit {
		return nil
	}
	switch b.OnExceed {
	case BudgetBlock:
		return &ProcessError{
			ProcessID: p.ID,
			AgentName: p.Agent.Name,
			Err:       ErrBudgetExceeded,
		}
	case BudgetWarn:
		slog.Warn("agent budget exceeded (allowing)",
			"process_id", p.ID,
			"agent", p.Agent.Name,
			"spent_usd", spent,
			"limit_usd", b.Limit,
		)
	case BudgetAllow:
		// Silently allow.
	}
	return nil
}

func (p *Process) callLLMWithRetry(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	// Budget check: enforce the agent's cost limit before spending more.
	// Checked first so a blocked budget doesn't consume a rate-limit token.
	if err := p.checkBudget(); err != nil {
		return nil, err
	}

	// Circuit breaker check
	if p.circuitBreaker != nil && !p.circuitBreaker.Allow() {
		return nil, &ProcessError{
			ProcessID: p.ID,
			AgentName: p.Agent.Name,
			Err:       ErrCircuitOpen,
		}
	}

	// Rate limiter: wait for a token if needed
	if p.rateLimiter != nil {
		if wait := p.rateLimiter.WaitTime(); wait > 0 {
			slog.Debug("rate limit: waiting for token",
				"process_id", p.ID,
				"agent", p.Agent.Name,
				"wait_ms", wait.Milliseconds(),
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		if !p.rateLimiter.Allow() {
			return nil, &ProcessError{
				ProcessID: p.ID,
				AgentName: p.Agent.Name,
				Err:       ErrRateLimited,
			}
		}
	}

	policy := p.Agent.Retry
	maxAttempts := 1
	if policy != nil && policy.MaxAttempts > 0 {
		maxAttempts = policy.MaxAttempts
	}

	stepType := p.stepTypeFor(messages)
	chosenModel := p.Agent.ModelFor(stepType)

	// Per-model rate limiting (orchestrator WithRateLimits). Reject when the
	// model's token bucket is empty rather than silently ignoring the config.
	if p.orchestrator != nil {
		if rl := p.orchestrator.rateLimiterFor(chosenModel); rl != nil && !rl.allow() {
			return nil, &ProcessError{
				ProcessID: p.ID,
				AgentName: p.Agent.Name,
				Err:       ErrRateLimited,
			}
		}
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		start := time.Now()
		resp, err := p.llm.Generate(p.llmCallContext(ctx, stepType), messages, tools)
		latency := time.Since(start)

		if err == nil {
			if p.circuitBreaker != nil {
				p.circuitBreaker.RecordSuccess()
			}
			slog.Debug("llm call succeeded",
				"process_id", p.ID,
				"agent", p.Agent.Name,
				"step_type", stepType,
				"model", chosenModel,
				"attempt", attempt+1,
				"latency_ms", latency.Milliseconds(),
				"input_tokens", resp.InputTokens,
				"output_tokens", resp.OutputTokens,
			)
			reportLLMCost(p, resp, stepType, chosenModel)
			return resp, nil
		}

		lastErr = err
		if p.circuitBreaker != nil {
			p.circuitBreaker.RecordFailure()
		}
		errClass := ClassifyError(err)

		slog.Warn("llm call failed",
			"process_id", p.ID,
			"agent", p.Agent.Name,
			"step_type", stepType,
			"model", chosenModel,
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

	// If a fallback model is configured, try once with it. The previous
	// implementation called Generate with the bare ctx, so the backend
	// never saw FallbackModel and silently used its default model.
	if p.Agent.FallbackModel != "" && p.Agent.FallbackModel != chosenModel {
		slog.Info("trying fallback model",
			"process_id", p.ID,
			"agent", p.Agent.Name,
			"step_type", stepType,
			"primary_model", chosenModel,
			"fallback_model", p.Agent.FallbackModel,
		)

		fallbackLLM := llm.New()
		fallbackCtx := llm.ContextWithOptions(ctx, p.Agent.fallbackOptions())
		start := time.Now()
		resp, err := fallbackLLM.Generate(fallbackCtx, messages, tools)
		latency := time.Since(start)

		if err == nil {
			slog.Info("fallback model succeeded",
				"process_id", p.ID,
				"agent", p.Agent.Name,
				"fallback_model", p.Agent.FallbackModel,
				"latency_ms", latency.Milliseconds(),
			)
			return resp, nil
		}

		slog.Warn("fallback model also failed",
			"process_id", p.ID,
			"agent", p.Agent.Name,
			"fallback_model", p.Agent.FallbackModel,
			"error", err.Error(),
		)
		lastErr = err
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
