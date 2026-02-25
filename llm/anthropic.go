// Package llm provides LLM backend implementations.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// AnthropicLLM is an LLM implementation using the Anthropic API.
type AnthropicLLM struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	model      string
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

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) AnthropicOption {
	return func(a *AnthropicLLM) {
		a.httpClient = client
	}
}

// Default Anthropic configuration values
const (
	DefaultAnthropicTimeout = 5 * time.Minute
	DefaultAnthropicModel   = "claude-sonnet-4-20250514"
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
)

// NewAnthropic creates a new Anthropic LLM client.
func NewAnthropic(opts ...AnthropicOption) *AnthropicLLM {
	a := &AnthropicLLM{
		apiKey:  os.Getenv("ANTHROPIC_API_KEY"),
		baseURL: DefaultAnthropicBaseURL,
		httpClient: &http.Client{
			Timeout: DefaultAnthropicTimeout,
		},
		model: DefaultAnthropicModel,
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

// anthropicRequest is the API request format.
type anthropicRequest struct {
	Model       string           `json:"model"`
	Messages    []anthropicMsg   `json:"messages"`
	System      any              `json:"system,omitempty"` // string or []systemBlock
	MaxTokens   int              `json:"max_tokens"`
	Temperature *float64         `json:"temperature,omitempty"`
	Tools       []anthropicTool  `json:"tools,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
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
	req := a.buildRequest(messages, tools, false)

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
	req := a.buildRequest(messages, tools, true)

	// Make streaming request
	eventCh := make(chan StreamEvent, 100)

	go func() {
		defer close(eventCh)

		const maxRetries = 5
		for attempt := 0; attempt <= maxRetries; attempt++ {
			httpReq, err := a.createHTTPRequest(ctx, req)
			if err != nil {
				eventCh <- StreamEvent{Type: StreamEventError, Error: err}
				return
			}

			httpResp, err := a.httpClient.Do(httpReq)
			if err != nil {
				eventCh <- StreamEvent{Type: StreamEventError, Error: err}
				return
			}

			if httpResp.StatusCode == http.StatusOK {
				a.parseSSE(httpResp.Body, eventCh)
				httpResp.Body.Close()
				return
			}

			body, _ := io.ReadAll(httpResp.Body)

			// Retry on 429 (rate limit) and 529 (overloaded).
			if (httpResp.StatusCode == 429 || httpResp.StatusCode == 529) && attempt < maxRetries {
				wait := retryAfterDelay(httpResp, attempt)
				slog.Warn("API rate limited (stream), retrying", "status", httpResp.StatusCode, "attempt", attempt+1, "wait", wait)
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

func (a *AnthropicLLM) buildRequest(messages []Message, tools []ToolSchema, stream bool) *anthropicRequest {
	req := &anthropicRequest{
		Model:     a.model,
		MaxTokens: 8192,
		Stream:    stream,
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

		// Messages containing <tool_use> or <tool_result> XML need to be
		// converted into structured content blocks for the Anthropic API.
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
	resultContent := strings.TrimSpace(s[tagEnd+1 : endIdx])

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
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	return httpReq, nil
}

func (a *AnthropicLLM) doRequest(ctx context.Context, req *anthropicRequest) (*anthropicResponse, error) {
	const maxRetries = 5

	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := a.createHTTPRequest(ctx, req)
		if err != nil {
			return nil, err
		}

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
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

		// Retry on 429 (rate limit) and 529 (overloaded).
		if (httpResp.StatusCode == 429 || httpResp.StatusCode == 529) && attempt < maxRetries {
			wait := retryAfterDelay(httpResp, attempt)
			slog.Warn("API rate limited, retrying", "status", httpResp.StatusCode, "attempt", attempt+1, "wait", wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		return nil, fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("max retries exceeded")
}

// retryAfterDelay returns how long to wait before retrying a rate-limited request.
// It respects the retry-after header if present, otherwise uses exponential backoff.
func retryAfterDelay(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("retry-after"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	// Exponential backoff: 5s, 10s, 20s, 40s, 60s
	wait := time.Duration(5<<uint(attempt)) * time.Second
	if wait > 60*time.Second {
		wait = 60 * time.Second
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
	}

	// Parse content blocks
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return result, nil
}

func (a *AnthropicLLM) parseSSE(reader io.Reader, eventCh chan<- StreamEvent) {
	scanner := bufio.NewScanner(reader)
	var currentEvent string
	var currentData strings.Builder

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
			a.processSSEEvent(currentEvent, currentData.String(), eventCh)
			currentEvent = ""
			currentData.Reset()
		}
	}
}

func (a *AnthropicLLM) processSSEEvent(eventType, data string, eventCh chan<- StreamEvent) {
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
		if block.ContentBlock.Type == "tool_use" {
			eventCh <- StreamEvent{
				Type: StreamEventToolStart,
				ToolCall: &ToolCall{
					ID:        block.ContentBlock.ID,
					Name:      block.ContentBlock.Name,
					Arguments: make(map[string]any),
				},
			}
		} else {
			eventCh <- StreamEvent{Type: StreamEventContentStart}
		}

	case "content_block_delta":
		var delta struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
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
