// Package llm provides LLM backend implementations.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxConcurrent is the default maximum number of in-flight API requests.
const DefaultMaxConcurrent = 5

// AnthropicLLM is an LLM implementation using the Anthropic API.
type AnthropicLLM struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	// streamClient serves the streaming path. It carries no overall
	// Timeout: http.Client.Timeout spans the entire response body, so a
	// fixed timeout silently cuts long streams mid-answer. Stream
	// cancellation is the caller's context.
	streamClient *http.Client
	model        string
	effort       string        // "" | "low" | "medium" | "high" | "xhigh" | "max"
	semaphore    chan struct{} // limits concurrent API requests
	// retryBase is the exponential-backoff base for retries (default 5s
	// when zero). Tests shrink it.
	retryBase time.Duration
}

// AnthropicOption configures the Anthropic client.
type AnthropicOption func(*AnthropicLLM)

// WithAPIKey sets the API key.
func WithAPIKey(key string) AnthropicOption {
	return func(a *AnthropicLLM) {
		a.apiKey = key
	}
}

// WithModel sets the default model.
func WithModel(model string) AnthropicOption {
	return func(a *AnthropicLLM) {
		a.model = model
	}
}

// WithBaseURL sets the API base URL.
func WithBaseURL(url string) AnthropicOption {
	return func(a *AnthropicLLM) {
		a.baseURL = url
	}
}

// WithHTTPClient sets a custom HTTP client. It is used for both the
// non-streaming and streaming paths — callers overriding it own the
// timeout trade-off (a non-zero Timeout will cut streams that outlive it).
func WithHTTPClient(client *http.Client) AnthropicOption {
	return func(a *AnthropicLLM) {
		a.httpClient = client
		a.streamClient = client
	}
}

// WithMaxConcurrent sets the maximum number of concurrent API requests.
func WithMaxConcurrent(n int) AnthropicOption {
	return func(a *AnthropicLLM) {
		if n > 0 {
			a.semaphore = make(chan struct{}, n)
		}
	}
}

// WithEffort sets output_config.effort for requests on capable models.
// Valid values: "low", "medium", "high", "xhigh", "max". Empty means
// "high" by default. "xhigh" is the recommended setting for agentic
// and coding workloads on Opus 4.7. "max" is Opus-tier only.
func WithEffort(effort string) AnthropicOption {
	return func(a *AnthropicLLM) {
		a.effort = effort
	}
}

// Default Anthropic configuration values
const (
	DefaultAnthropicTimeout = 5 * time.Minute
	DefaultAnthropicModel   = "claude-sonnet-4-6"
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
)

// NewAnthropic creates a new Anthropic LLM client.
func NewAnthropic(opts ...AnthropicOption) *AnthropicLLM {
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}

	a := &AnthropicLLM{
		apiKey:  os.Getenv("ANTHROPIC_API_KEY"),
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: DefaultAnthropicTimeout,
		},
		// No Timeout on the streaming client — see the field comment.
		streamClient: &http.Client{},
		model:        DefaultAnthropicModel,
		semaphore:    make(chan struct{}, DefaultMaxConcurrent),
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// cacheControl marks a block for Anthropic prompt caching.
type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// systemBlock is a structured system prompt block with optional cache control.
type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// thinkingBlock configures adaptive thinking for the API request.
// Older budget_tokens form is removed on Opus 4.7 and deprecated on 4.6.
type thinkingBlock struct {
	Type string `json:"type"` // "adaptive"
}

// outputConfig carries effort and structured-output controls. Each field
// is gated separately by capability — effort errors on Sonnet 4.5 /
// Haiku 4.5; format errors on models that don't support structured
// outputs. Either field may appear without the other.
type outputConfig struct {
	Effort string        `json:"effort,omitempty"` // "low" | "medium" | "high" | "xhigh" | "max"
	Format *outputFormat `json:"format,omitempty"`
}

// outputFormat enforces a JSON Schema on the model response.
type outputFormat struct {
	Type   string         `json:"type"` // "json_schema"
	Schema map[string]any `json:"schema"`
}

// anthropicRequest is the API request format.
type anthropicRequest struct {
	Model        string          `json:"model"`
	Messages     []anthropicMsg  `json:"messages"`
	System       any             `json:"system,omitempty"` // string or []systemBlock
	MaxTokens    int             `json:"max_tokens"`
	Temperature  *float64        `json:"temperature,omitempty"`
	Tools        []anthropicTool `json:"tools,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	Thinking     *thinkingBlock  `json:"thinking,omitempty"`
	OutputConfig *outputConfig   `json:"output_config,omitempty"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	Signature string         `json:"signature,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}


type anthropicTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

// anthropicResponse is the API response format.
type anthropicResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []contentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence string         `json:"stop_sequence"`
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// ValidateKey makes a minimal API call to verify the API key is valid.
// Returns nil on success, or an error describing the failure (empty key,
// authentication failure, or network/other error).
func (a *AnthropicLLM) ValidateKey(ctx context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("API key is empty")
	}

	req := &anthropicRequest{
		Model:     a.model,
		MaxTokens: 1,
		Messages:  []anthropicMsg{{Role: "user", Content: "hi"}},
	}

	_, err := a.doRequest(ctx, req)
	if err == nil {
		return nil
	}

	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "invalid") || strings.Contains(errStr, "authentication") {
		return fmt.Errorf("invalid API key: %w", err)
	}
	return fmt.Errorf("could not reach Anthropic API: %w", err)
}

// Generate sends a request and returns the complete response.
func (a *AnthropicLLM) Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error) {
	start := time.Now()

	// Build request
	req := a.buildRequestCtx(ctx, messages, tools, false)

	// Make request
	resp, err := a.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	// Parse response
	return a.parseResponse(resp, time.Since(start))
}

// GenerateStream sends a request and returns a channel of streaming events.
func (a *AnthropicLLM) GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error) {
	// Build request
	req := a.buildRequestCtx(ctx, messages, tools, true)

	// Make streaming request
	eventCh := make(chan StreamEvent, 100)

	go func() {
		defer close(eventCh)

		// Acquire concurrency semaphore.
		select {
		case a.semaphore <- struct{}{}:
			defer func() { <-a.semaphore }()
		case <-ctx.Done():
			eventCh <- StreamEvent{Type: StreamEventError, Error: ctx.Err()}
			return
		}

		const maxRetries = 5
		for attempt := 0; attempt <= maxRetries; attempt++ {
			httpReq, err := a.createHTTPRequest(ctx, req)
			if err != nil {
				eventCh <- StreamEvent{Type: StreamEventError, Error: err}
				return
			}

			client := a.streamClient
			if client == nil {
				client = a.httpClient
			}
			httpResp, err := client.Do(httpReq)
			if err != nil {
				// Transport failure before the stream opened — retry.
				if ctx.Err() == nil && attempt < maxRetries {
					wait := retryAfterDelay(nil, attempt, a.retryBase)
					slog.Warn("API request failed (stream), retrying", "error", err, "attempt", attempt+1, "wait", wait)
					select {
					case <-time.After(wait):
						continue
					case <-ctx.Done():
					}
				}
				eventCh <- StreamEvent{Type: StreamEventError, Error: err}
				return
			}

			if httpResp.StatusCode == http.StatusOK {
				parseErr := a.parseSSE(httpResp.Body, eventCh, req.Model)
				httpResp.Body.Close()
				if parseErr != nil {
					eventCh <- StreamEvent{Type: StreamEventError, Error: parseErr}
				}
				return
			}

			body, _ := io.ReadAll(httpResp.Body)

			// Retry transient statuses before the stream opened: 429, 529, 5xx.
			if isRetryableStatus(httpResp.StatusCode) && attempt < maxRetries {
				wait := retryAfterDelay(httpResp, attempt, a.retryBase)
				slog.Warn("API transient error (stream), retrying", "status", httpResp.StatusCode, "attempt", attempt+1, "wait", wait)
				httpResp.Body.Close()
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					eventCh <- StreamEvent{Type: StreamEventError, Error: ctx.Err()}
					return
				}
			}

			httpResp.Body.Close()
			slog.Error("anthropic API error (stream)",
				"status", httpResp.StatusCode,
				"body", string(body),
				"model", req.Model,
				"url", a.baseURL+"/v1/messages",
				"request_headers", fmt.Sprintf("%v", httpReq.Header),
				"stream", req.Stream,
				"thinking", req.Thinking != nil,
				"tools", len(req.Tools),
			)
			eventCh <- StreamEvent{
				Type:  StreamEventError,
				Error: fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(body)),
			}
			return
		}

		eventCh <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("max retries exceeded")}
	}()

	return eventCh, nil
}

// nonStreamMaxTokensCap bounds non-streaming output to keep the response
// under the 5-minute SDK HTTP timeout. Streaming requests are allowed
// the model's full ceiling.
const nonStreamMaxTokensCap = 16000

// buildRequest is a thin wrapper that uses a background context — kept
// for callers that don't need per-call overrides.
func (a *AnthropicLLM) buildRequest(messages []Message, tools []ToolSchema, stream bool) *anthropicRequest {
	return a.buildRequestCtx(context.Background(), messages, tools, stream)
}

func (a *AnthropicLLM) buildRequestCtx(ctx context.Context, messages []Message, tools []ToolSchema, stream bool) *anthropicRequest {
	opts := OptionsFromContext(ctx)

	model := a.model
	if opts.Model != "" {
		model = opts.Model
	}
	caps := CapabilitiesFor(model)

	maxTokens := 8192
	if caps.MaxOutputTokens > 0 {
		maxTokens = caps.MaxOutputTokens
		if !stream {
			maxTokens = min(maxTokens, nonStreamMaxTokensCap)
		}
	}
	if opts.MaxTokens > 0 {
		maxTokens = opts.MaxTokens
	}

	req := &anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Stream:    stream,
	}

	if caps.AdaptiveThinking {
		req.Thinking = &thinkingBlock{Type: "adaptive"}
	}

	if caps.SupportsEffort {
		effort := opts.Effort
		if effort == "" {
			effort = a.effort
		}
		if effort == "" {
			effort = "high"
		}
		req.OutputConfig = &outputConfig{Effort: effort}
	}

	if caps.SupportsStructuredOutputs && opts.OutputSchema != nil {
		if req.OutputConfig == nil {
			req.OutputConfig = &outputConfig{}
		}
		req.OutputConfig.Format = &outputFormat{
			Type:   "json_schema",
			Schema: opts.OutputSchema,
		}
	}

	if caps.SupportsTemperature && opts.Temperature != nil {
		req.Temperature = opts.Temperature
	}

	// Extract system message and convert others
	var anthropicMsgs []anthropicMsg
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			req.System = []systemBlock{{
				Type:         "text",
				Text:         msg.Content,
				CacheControl: &cacheControl{Type: "ephemeral"},
			}}
			continue
		}

		// Typed content blocks convert directly — no markup round trip.
		if len(msg.Blocks) > 0 {
			if blocks := blocksToAnthropic(msg.Blocks); len(blocks) > 0 {
				anthropicMsgs = append(anthropicMsgs, anthropicMsg{
					Role:    string(msg.Role),
					Content: blocks,
				})
			}
			continue
		}

		// Legacy path: messages containing <tool_use> or <tool_result> XML
		// (persisted history from older versions) are re-parsed into
		// structured content blocks.
		if strings.Contains(msg.Content, "<tool_use ") || strings.Contains(msg.Content, "<tool_result ") {
			blocks := parseToolBlocks(msg.Content)
			if len(blocks) > 0 {
				anthropicMsgs = append(anthropicMsgs, anthropicMsg{
					Role:    string(msg.Role),
					Content: blocks,
				})
				continue
			}
		}

		anthropicMsgs = append(anthropicMsgs, anthropicMsg{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}
	// Cache the trailing message so the next call hits cache up through
	// this turn (refs govega#3). Standard rolling pattern: each call
	// writes cache at the end of the conversation; the next call reads
	// it. Works whether the last message is a plain text turn or a
	// structured tool_result. Caps total breakpoints at 4 (Anthropic
	// limit) — we hold 3: system, last tool, trailing message.
	if n := len(anthropicMsgs); n > 0 {
		markTrailingMessageForCache(&anthropicMsgs[n-1])
	}
	req.Messages = anthropicMsgs

	// Convert tools and mark the last one with cache_control to cache the
	// entire prefix (system + tools) for prompt caching.
	if len(tools) > 0 {
		for i, t := range tools {
			at := anthropicTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			}
			if i == len(tools)-1 {
				at.CacheControl = &cacheControl{Type: "ephemeral"}
			}
			req.Tools = append(req.Tools, at)
		}
	}

	return req
}

// markTrailingMessageForCache adds an ephemeral cache_control marker to
// the last content block in msg (refs govega#3). For string content the
// message is promoted to a single text block carrying the marker — the
// API rejects cache_control on a bare string. For structured content
// the marker lands on the trailing block (typically a tool_result or
// assistant text). This is the third breakpoint alongside system and
// the last tool definition.
func markTrailingMessageForCache(msg *anthropicMsg) {
	switch content := msg.Content.(type) {
	case string:
		if content == "" {
			return
		}
		msg.Content = []any{map[string]any{
			"type":          "text",
			"text":          content,
			"cache_control": map[string]any{"type": "ephemeral"},
		}}
	case []any:
		if len(content) == 0 {
			return
		}
		last, ok := content[len(content)-1].(map[string]any)
		if !ok {
			return
		}
		last["cache_control"] = map[string]any{"type": "ephemeral"}
	}
}

// blocksToAnthropic converts typed ContentBlocks into Anthropic API content
// blocks. API-invalid blocks are dropped: empty text blocks are rejected by
// the API, and thinking blocks without a signature cannot be replayed.
// Returns []any of maps so markTrailingMessageForCache can annotate the
// trailing block.
func blocksToAnthropic(blocks []ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			if b.Text == "" {
				continue
			}
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case BlockThinking:
			if b.Signature == "" {
				continue
			}
			out = append(out, map[string]any{
				"type":      "thinking",
				"thinking":  b.Text,
				"signature": b.Signature,
			})
		case BlockToolUse:
			args := b.Arguments
			if args == nil {
				args = map[string]any{}
			}
			out = append(out, map[string]any{
				"type":  "tool_use",
				"id":    b.ID,
				"name":  b.Name,
				"input": args,
			})
		case BlockToolResult:
			blk := map[string]any{
				"type":        "tool_result",
				"tool_use_id": b.ToolUseID,
				"content":     b.Content,
			}
			if b.IsError {
				blk["is_error"] = true
			}
			out = append(out, blk)
		}
	}
	return out
}

// parseToolBlocks converts message text containing XML tool_use/tool_result
// tags into structured Anthropic content blocks for API requests.
// Returns []any where each element is a map with exactly the fields the API
// expects for that block type (text, tool_use, or tool_result).
func parseToolBlocks(content string) []any {
	var blocks []any

	remaining := content
	for remaining != "" {
		toolUseIdx := strings.Index(remaining, "<tool_use ")
		toolResultIdx := strings.Index(remaining, "<tool_result ")

		nextIdx := -1
		isToolUse := false
		if toolUseIdx >= 0 && (toolResultIdx < 0 || toolUseIdx < toolResultIdx) {
			nextIdx = toolUseIdx
			isToolUse = true
		} else if toolResultIdx >= 0 {
			nextIdx = toolResultIdx
		}

		if nextIdx < 0 {
			text := strings.TrimSpace(remaining)
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			break
		}

		if nextIdx > 0 {
			text := strings.TrimSpace(remaining[:nextIdx])
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
		}

		if isToolUse {
			block, rest := parseToolUseXML(remaining[nextIdx:])
			if block != nil {
				blocks = append(blocks, block)
			}
			remaining = rest
		} else {
			block, rest := parseToolResultXML(remaining[nextIdx:])
			if block != nil {
				blocks = append(blocks, block)
			}
			remaining = rest
		}
	}

	return blocks
}

// parseToolUseXML extracts a tool_use block from XML like:
// <tool_use id="..." name="...">\njson\n</tool_use>
func parseToolUseXML(s string) (map[string]any, string) {
	endTag := "</tool_use>"
	endIdx := strings.Index(s, endTag)
	if endIdx < 0 {
		return nil, ""
	}

	tagEnd := strings.Index(s, ">")
	if tagEnd < 0 || tagEnd > endIdx {
		return nil, s[endIdx+len(endTag):]
	}

	openTag := s[:tagEnd]
	id := extractAttr(openTag, "id")
	name := extractAttr(openTag, "name")
	jsonBody := strings.TrimSpace(s[tagEnd+1 : endIdx])

	input := map[string]any{}
	if jsonBody != "" {
		json.Unmarshal([]byte(jsonBody), &input)
	}

	block := map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": input,
	}

	return block, s[endIdx+len(endTag):]
}

// parseToolResultXML extracts a tool_result block from XML like:
// <tool_result tool_use_id="..." name="...">\ncontent\n</tool_result>
func parseToolResultXML(s string) (map[string]any, string) {
	endTag := "</tool_result>"
	endIdx := strings.Index(s, endTag)
	if endIdx < 0 {
		return nil, ""
	}

	tagEnd := strings.Index(s, ">")
	if tagEnd < 0 || tagEnd > endIdx {
		return nil, s[endIdx+len(endTag):]
	}

	openTag := s[:tagEnd]
	toolUseID := extractAttr(openTag, "tool_use_id")
	// The producer HTML-escapes the payload so it can't contain literal tag
	// sequences that break out of the block (history-injection guard). Restore
	// the original content for the API.
	resultContent := html.UnescapeString(strings.TrimSpace(s[tagEnd+1 : endIdx]))

	block := map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     resultContent,
	}

	return block, s[endIdx+len(endTag):]
}

// extractAttr extracts an attribute value from an XML-like tag string.
// e.g. extractAttr(`<tool_use id="abc" name="foo"`, "id") → "abc"
func extractAttr(tag, attr string) string {
	needle := attr + `="`
	idx := strings.Index(tag, needle)
	if idx < 0 {
		return ""
	}
	start := idx + len(needle)
	end := strings.Index(tag[start:], `"`)
	if end < 0 {
		return ""
	}
	return tag[start : start+end]
}

func (a *AnthropicLLM) createHTTPRequest(ctx context.Context, req *anthropicRequest) (*http.Request, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.resolveAPIKey(ctx))
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	slog.Debug("anthropic request",
		"model", req.Model,
		"url", a.baseURL+"/v1/messages",
		"anthropic-version", httpReq.Header.Get("anthropic-version"),
		"stream", req.Stream,
		"thinking", req.Thinking != nil,
		"tools", len(req.Tools),
		"messages", len(req.Messages),
		"body_bytes", len(body),
	)

	return httpReq, nil
}

func (a *AnthropicLLM) doRequest(ctx context.Context, req *anthropicRequest) (*anthropicResponse, error) {
	// Acquire concurrency semaphore.
	select {
	case a.semaphore <- struct{}{}:
		defer func() { <-a.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	const maxRetries = 5

	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := a.createHTTPRequest(ctx, req)
		if err != nil {
			return nil, err
		}

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
			// Transport-level failure (connection reset, DNS blip):
			// transient — retry unless the caller's context is done.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < maxRetries {
				wait := retryAfterDelay(nil, attempt, a.retryBase)
				slog.Warn("API request failed, retrying", "error", err, "attempt", attempt+1, "wait", wait)
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, fmt.Errorf("http request: %w", err)
		}

		body, err := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if httpResp.StatusCode == http.StatusOK {
			var resp anthropicResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return nil, fmt.Errorf("unmarshal response: %w", err)
			}
			return &resp, nil
		}

		// Retry transient statuses: 429 rate limit, 529 overloaded, 5xx.
		if isRetryableStatus(httpResp.StatusCode) && attempt < maxRetries {
			wait := retryAfterDelay(httpResp, attempt, a.retryBase)
			slog.Warn("API transient error, retrying", "status", httpResp.StatusCode, "attempt", attempt+1, "wait", wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		slog.Error("anthropic API error",
			"status", httpResp.StatusCode,
			"body", string(body),
			"model", req.Model,
			"url", a.baseURL+"/v1/messages",
			"request_headers", fmt.Sprintf("%v", httpReq.Header),
			"stream", req.Stream,
			"thinking", req.Thinking != nil,
			"tools", len(req.Tools),
		)
		return nil, fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("max retries exceeded")
}

// isRetryableStatus reports whether an HTTP status is a transient failure
// worth retrying: rate limits (429), Anthropic overload (529), and server
// errors (500/502/503/504). 4xx client errors are never retried.
func isRetryableStatus(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// retryAfterDelay returns how long to wait before retrying a transient
// failure. It respects the retry-after header if present, otherwise uses
// exponential backoff from base (5s when base is zero): 5s, 10s, 20s, ….
func retryAfterDelay(resp *http.Response, attempt int, base time.Duration) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("retry-after"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	if base <= 0 {
		base = 5 * time.Second
	}
	wait := base << uint(attempt)
	if max := base * 12; wait > max {
		wait = max
	}
	return wait
}

func (a *AnthropicLLM) parseResponse(resp *anthropicResponse, latency time.Duration) (*LLMResponse, error) {
	result := &LLMResponse{
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		LatencyMs:                latency.Milliseconds(),
	}

	if resp.Usage.CacheReadInputTokens > 0 || resp.Usage.CacheCreationInputTokens > 0 {
		slog.Debug("prompt cache", "read", resp.Usage.CacheReadInputTokens, "created", resp.Usage.CacheCreationInputTokens)
	}

	// Calculate cost (including cache token costs)
	result.CostUSD = CalculateCost(resp.Model, result.InputTokens, result.OutputTokens,
		result.CacheCreationInputTokens, result.CacheReadInputTokens)

	// Parse stop reason
	switch resp.StopReason {
	case "end_turn":
		result.StopReason = StopReasonEnd
	case "tool_use":
		result.StopReason = StopReasonToolUse
	case "max_tokens":
		result.StopReason = StopReasonLength
	case "stop_sequence":
		result.StopReason = StopReasonStop
	case "pause_turn":
		result.StopReason = StopReasonPause
	case "refusal":
		result.StopReason = StopReasonRefusal
	case "model_context_window_exceeded":
		result.StopReason = StopReasonContextExceeded
	}

	// Parse content blocks — kept both as the legacy flat fields and as
	// ordered typed Blocks so callers can replay the turn losslessly.
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
			result.Blocks = append(result.Blocks, ContentBlock{Type: BlockText, Text: block.Text})
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
			result.Blocks = append(result.Blocks, ContentBlock{
				Type:      BlockToolUse,
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		case "thinking":
			slog.Debug("thinking block", "length", len(block.Thinking))
			result.Blocks = append(result.Blocks, ContentBlock{
				Type:      BlockThinking,
				Text:      block.Thinking,
				Signature: block.Signature,
			})
		}
	}

	return result, nil
}

// maxSSELineBytes bounds a single SSE line. bufio.Scanner's 64KB default
// is too small for large data payloads (big tool inputs, long documents);
// exceeding it used to abort the stream silently.
const maxSSELineBytes = 10 * 1024 * 1024

// parseSSE consumes the SSE body and emits StreamEvents. It returns an
// error when the stream ends abnormally — a read error (network cut,
// timeout) or a connection that closed before the server's message_stop —
// so the caller can surface truncation instead of returning a partial
// answer as if it were complete.
func (a *AnthropicLLM) parseSSE(reader io.Reader, eventCh chan<- StreamEvent, model string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxSSELineBytes)
	var currentEvent string
	var currentData strings.Builder
	sawMessageStop := false
	st := &sseState{model: model}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			currentData.WriteString(strings.TrimPrefix(line, "data: "))
			continue
		}

		if line == "" && currentEvent != "" {
			// Process complete event
			if currentEvent == "message_stop" {
				sawMessageStop = true
			}
			a.processSSEEvent(currentEvent, currentData.String(), eventCh, st)
			currentEvent = ""
			currentData.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read failed: %w", err)
	}
	if !sawMessageStop {
		return fmt.Errorf("stream truncated: connection closed before message_stop")
	}
	return nil
}

// sseState carries per-stream accounting so the message_end event can
// report cost computed from the model actually used for the request.
type sseState struct {
	model                    string
	inputTokens              int
	cacheCreationInputTokens int
	cacheReadInputTokens     int
}

func (a *AnthropicLLM) processSSEEvent(eventType, data string, eventCh chan<- StreamEvent, st *sseState) {
	switch eventType {
	case "message_start":
		var msg struct {
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		json.Unmarshal([]byte(data), &msg)
		st.inputTokens = msg.Message.Usage.InputTokens
		st.cacheCreationInputTokens = msg.Message.Usage.CacheCreationInputTokens
		st.cacheReadInputTokens = msg.Message.Usage.CacheReadInputTokens
		eventCh <- StreamEvent{
			Type:                     StreamEventMessageStart,
			InputTokens:              msg.Message.Usage.InputTokens,
			CacheCreationInputTokens: msg.Message.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     msg.Message.Usage.CacheReadInputTokens,
		}

	case "content_block_start":
		var block struct {
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		json.Unmarshal([]byte(data), &block)
		switch block.ContentBlock.Type {
		case "tool_use":
			eventCh <- StreamEvent{
				Type: StreamEventToolStart,
				ToolCall: &ToolCall{
					ID:        block.ContentBlock.ID,
					Name:      block.ContentBlock.Name,
					Arguments: make(map[string]any),
				},
			}
		case "thinking":
			eventCh <- StreamEvent{Type: StreamEventThinkingStart}
		default:
			eventCh <- StreamEvent{Type: StreamEventContentStart}
		}

	case "content_block_delta":
		var delta struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		json.Unmarshal([]byte(data), &delta)
		switch delta.Delta.Type {
		case "text_delta":
			eventCh <- StreamEvent{
				Type:  StreamEventContentDelta,
				Delta: delta.Delta.Text,
			}
		case "input_json_delta":
			eventCh <- StreamEvent{
				Type:  StreamEventToolDelta,
				Delta: delta.Delta.PartialJSON,
			}
		case "thinking_delta":
			// Forwarded (not shown to users) so callers can replay the
			// thinking block on the next request of the turn.
			eventCh <- StreamEvent{
				Type:  StreamEventThinkingDelta,
				Delta: delta.Delta.Thinking,
			}
		case "signature_delta":
			eventCh <- StreamEvent{
				Type:  StreamEventThinkingSignature,
				Delta: delta.Delta.Signature,
			}
		}

	case "content_block_stop":
		eventCh <- StreamEvent{Type: StreamEventContentEnd}

	case "message_delta":
		var delta struct {
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal([]byte(data), &delta)
		eventCh <- StreamEvent{
			Type:         StreamEventMessageEnd,
			OutputTokens: delta.Usage.OutputTokens,
			CostUSD: CalculateCost(st.model, st.inputTokens, delta.Usage.OutputTokens,
				st.cacheCreationInputTokens, st.cacheReadInputTokens),
		}

	case "message_stop":
		// Final event, no action needed

	case "error":
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal([]byte(data), &errResp)
		eventCh <- StreamEvent{
			Type:  StreamEventError,
			Error: fmt.Errorf("stream error: %s", errResp.Error.Message),
		}
	}
}
