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
	"strings"
	"time"
)

// OpenAILLM is an LLM implementation using the OpenAI-compatible chat completions API.
// Works with LiteLLM, Ollama, vLLM, and any OpenAI-compatible endpoint.
type OpenAILLM struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	model      string
	semaphore  chan struct{}
}

// OpenAIOption configures the OpenAI-compatible client.
type OpenAIOption func(*OpenAILLM)

// WithOpenAIAPIKey sets the API key.
func WithOpenAIAPIKey(key string) OpenAIOption {
	return func(o *OpenAILLM) { o.apiKey = key }
}

// WithOpenAIModel sets the default model.
func WithOpenAIModel(model string) OpenAIOption {
	return func(o *OpenAILLM) { o.model = model }
}

// WithOpenAIBaseURL sets the API base URL.
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(o *OpenAILLM) { o.baseURL = url }
}

const (
	DefaultOpenAIModel   = "qwen-coder"
	DefaultOpenAIBaseURL = "http://localhost:4000"
)

// NewOpenAI creates a new OpenAI-compatible LLM client.
func NewOpenAI(opts ...OpenAIOption) *OpenAILLM {
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = DefaultOpenAIModel
	}

	apiKey := os.Getenv("VEGA_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		apiKey = "sk-local"
	}

	o := &OpenAILLM{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		model:     model,
		semaphore: make(chan struct{}, DefaultMaxConcurrent),
	}

	for _, opt := range opts {
		opt(o)
	}

	return o
}

// OpenAI request/response types

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMsg     `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openaiMsg struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []openaiToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openaiStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Generate sends a request and returns the complete response.
func (o *OpenAILLM) Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error) {
	start := time.Now()

	req := o.buildRequest(messages, tools, false)

	resp, err := o.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return o.parseResponse(resp, time.Since(start))
}

// GenerateStream sends a request and returns a channel of streaming events.
func (o *OpenAILLM) GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error) {
	req := o.buildRequest(messages, tools, true)

	eventCh := make(chan StreamEvent, 100)

	go func() {
		defer close(eventCh)

		select {
		case o.semaphore <- struct{}{}:
			defer func() { <-o.semaphore }()
		case <-ctx.Done():
			eventCh <- StreamEvent{Type: StreamEventError, Error: ctx.Err()}
			return
		}

		httpReq, err := o.createHTTPRequest(ctx, req)
		if err != nil {
			eventCh <- StreamEvent{Type: StreamEventError, Error: err}
			return
		}

		httpResp, err := o.httpClient.Do(httpReq)
		if err != nil {
			eventCh <- StreamEvent{Type: StreamEventError, Error: err}
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(httpResp.Body)
			slog.Error("openai API error (stream)", "status", httpResp.StatusCode, "body", string(body))
			eventCh <- StreamEvent{
				Type:  StreamEventError,
				Error: fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(body)),
			}
			return
		}

		o.parseStreamSSE(httpResp.Body, eventCh)
	}()

	return eventCh, nil
}

func (o *OpenAILLM) buildRequest(messages []Message, tools []ToolSchema, stream bool) *openaiRequest {
	req := &openaiRequest{
		Model:     o.model,
		MaxTokens: 8192,
		Stream:    stream,
	}

	// When tools are available, prepend an instruction that nudges
	// non-Anthropic models to actually invoke tools rather than just
	// describing what they would do.
	toolNudge := ""
	if len(tools) > 0 {
		toolNudge = "\n\nIMPORTANT: When you decide to use a tool, you MUST call it using the function calling mechanism. Do NOT describe tool calls in text or output JSON manually. Actually invoke the tool. Act, don't narrate."
	}

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			req.Messages = append(req.Messages, openaiMsg{
				Role:    "system",
				Content: msg.Content + toolNudge,
			})
			toolNudge = "" // Only add once.
			continue
		}

		// Parse tool XML blocks in messages, same as the Anthropic backend.
		if strings.Contains(msg.Content, "<tool_use ") || strings.Contains(msg.Content, "<tool_result ") {
			oaiMsgs := convertToolXMLToOpenAI(string(msg.Role), msg.Content)
			if len(oaiMsgs) > 0 {
				req.Messages = append(req.Messages, oaiMsgs...)
				continue
			}
		}

		req.Messages = append(req.Messages, openaiMsg{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	for _, t := range tools {
		req.Tools = append(req.Tools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return req
}

// convertToolXMLToOpenAI converts messages containing tool XML into proper
// OpenAI-format messages (assistant with tool_calls, tool with results).
func convertToolXMLToOpenAI(role, content string) []openaiMsg {
	var msgs []openaiMsg
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
				msgs = append(msgs, openaiMsg{Role: role, Content: text})
			}
			break
		}

		if nextIdx > 0 {
			text := strings.TrimSpace(remaining[:nextIdx])
			if text != "" {
				msgs = append(msgs, openaiMsg{Role: role, Content: text})
			}
		}

		if isToolUse {
			block, rest := parseToolUseXML(remaining[nextIdx:])
			if block != nil {
				argsJSON, _ := json.Marshal(block["input"])
				msgs = append(msgs, openaiMsg{
					Role: "assistant",
					ToolCalls: []openaiToolCall{{
						ID:   block["id"].(string),
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      block["name"].(string),
							Arguments: string(argsJSON),
						},
					}},
				})
			}
			remaining = rest
		} else {
			block, rest := parseToolResultXML(remaining[nextIdx:])
			if block != nil {
				msgs = append(msgs, openaiMsg{
					Role:       "tool",
					Content:    block["content"].(string),
					ToolCallID: block["tool_use_id"].(string),
				})
			}
			remaining = rest
		}
	}

	return msgs
}

func (o *OpenAILLM) createHTTPRequest(ctx context.Context, req *openaiRequest) (*http.Request, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := o.baseURL + "/v1/chat/completions"
	// If baseURL already ends with /v1, don't double it.
	if strings.HasSuffix(o.baseURL, "/v1") {
		url = o.baseURL + "/chat/completions"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	slog.Debug("openai request",
		"model", req.Model,
		"url", url,
		"stream", req.Stream,
		"tools", len(req.Tools),
		"messages", len(req.Messages),
	)

	return httpReq, nil
}

func (o *OpenAILLM) doRequest(ctx context.Context, req *openaiRequest) (*openaiResponse, error) {
	select {
	case o.semaphore <- struct{}{}:
		defer func() { <-o.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	httpReq, err := o.createHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	httpResp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	body, err := io.ReadAll(httpResp.Body)
	httpResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		slog.Error("openai API error", "status", httpResp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(body))
	}

	var resp openaiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

func (o *OpenAILLM) parseResponse(resp *openaiResponse, latency time.Duration) (*LLMResponse, error) {
	result := &LLMResponse{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		LatencyMs:    latency.Milliseconds(),
	}

	if len(resp.Choices) == 0 {
		return result, nil
	}

	choice := resp.Choices[0]
	result.Content = choice.Message.Content

	switch choice.FinishReason {
	case "stop":
		result.StopReason = StopReasonEnd
	case "tool_calls":
		result.StopReason = StopReasonToolUse
	case "length":
		result.StopReason = StopReasonLength
	case "content_filter":
		result.StopReason = StopReasonFiltered
	}

	for _, tc := range choice.Message.ToolCalls {
		args := map[string]any{}
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	// Fallback: local models often output tool calls as JSON text instead of
	// using the structured tool_calls field. Detect and convert them.
	if len(result.ToolCalls) == 0 && result.Content != "" {
		if tc, remaining := extractToolCallFromText(result.Content); tc != nil {
			result.ToolCalls = append(result.ToolCalls, *tc)
			result.Content = strings.TrimSpace(remaining)
			result.StopReason = StopReasonToolUse
		}
	}

	return result, nil
}

// extractToolCallFromText detects a JSON tool call in text content that local
// models produce instead of using the proper tool_calls response field.
// Recognises two common formats:
//
//	{"name": "tool", "parameters": {...}}
//	{"name": "tool", "arguments": {...}}
func extractToolCallFromText(text string) (*ToolCall, string) {
	trimmed := strings.TrimSpace(text)

	// Find the outermost JSON object.
	start := strings.IndexByte(trimmed, '{')
	if start < 0 {
		return nil, text
	}

	// Walk forward to find the matching closing brace.
	depth := 0
	end := -1
outer:
	for i := start; i < len(trimmed); i++ {
		switch trimmed[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				break outer
			}
		}
	}
	if end < 0 {
		return nil, text
	}

	jsonStr := trimmed[start:end]
	var obj map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return nil, text
	}

	name, _ := obj["name"].(string)
	if name == "" {
		return nil, text
	}

	// Accept "parameters" or "arguments" as the args field.
	args, _ := obj["parameters"].(map[string]any)
	if args == nil {
		args, _ = obj["arguments"].(map[string]any)
	}
	if args == nil {
		args = map[string]any{}
	}

	tc := &ToolCall{
		ID:        fmt.Sprintf("call_%d", time.Now().UnixNano()),
		Name:      name,
		Arguments: args,
	}

	remaining := strings.TrimSpace(trimmed[:start]) + strings.TrimSpace(trimmed[end:])
	return tc, remaining
}

func (o *OpenAILLM) parseStreamSSE(reader io.Reader, eventCh chan<- StreamEvent) {
	scanner := bufio.NewScanner(reader)
	sentStart := false

	// Track tool calls being built up across chunks.
	type toolState struct {
		id   string
		name string
		args strings.Builder
	}
	activeTools := map[int]*toolState{}

	// Accumulate text content for fallback tool-call-in-text detection.
	// We buffer output when it looks like a JSON tool call is forming so
	// the UI doesn't render raw JSON that will later become a tool call.
	var textAccum strings.Builder
	var buffered strings.Builder
	jsonLikely := false

	flush := func() {
		if buffered.Len() > 0 {
			eventCh <- StreamEvent{
				Type:  StreamEventContentDelta,
				Delta: buffered.String(),
			}
			buffered.Reset()
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Flush any active structured tool calls.
			// Emit ToolStart (with args in ID/Name only), then ContentEnd
			// to match the event sequence that process_llm.go expects:
			//   ToolStart → (ToolDelta)* → ContentEnd
			for _, ts := range activeTools {
				args := map[string]any{}
				json.Unmarshal([]byte(ts.args.String()), &args)
				eventCh <- StreamEvent{
					Type: StreamEventToolStart,
					ToolCall: &ToolCall{
						ID:        ts.id,
						Name:      ts.name,
						Arguments: args,
					},
				}
				eventCh <- StreamEvent{Type: StreamEventContentEnd}
			}

			// Fallback: if no structured tool calls were found, check if
			// the accumulated text contains a JSON tool call (common with
			// local models that don't support function calling).
			if len(activeTools) == 0 {
				if tc, _ := extractToolCallFromText(textAccum.String()); tc != nil {
					eventCh <- StreamEvent{
						Type:     StreamEventToolStart,
						ToolCall: tc,
					}
					eventCh <- StreamEvent{Type: StreamEventContentEnd}
				} else {
					// Not a tool call — flush any buffered text.
					flush()
				}
			}

			eventCh <- StreamEvent{Type: StreamEventMessageEnd}
			return
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Debug("openai stream: unmarshal error", "error", err, "data", data[:min(len(data), 200)])
			continue
		}

		if len(chunk.Choices) > 0 {
			d := chunk.Choices[0].Delta
			if len(d.ToolCalls) > 0 {
				slog.Info("openai stream: tool_call chunk", "id", d.ToolCalls[0].ID, "name", d.ToolCalls[0].Function.Name, "args_partial", d.ToolCalls[0].Function.Arguments[:min(len(d.ToolCalls[0].Function.Arguments), 100)])
			}
			if chunk.Choices[0].FinishReason != nil {
				slog.Info("openai stream: finish", "reason", *chunk.Choices[0].FinishReason, "active_tools", len(activeTools), "text_len", textAccum.Len())
			}
		}

		if !sentStart {
			evt := StreamEvent{Type: StreamEventMessageStart}
			if chunk.Usage != nil {
				evt.InputTokens = chunk.Usage.PromptTokens
			}
			eventCh <- evt
			sentStart = true
		}

		if len(chunk.Choices) == 0 {
			// Usage-only chunk at end.
			if chunk.Usage != nil {
				eventCh <- StreamEvent{
					Type:         StreamEventMessageEnd,
					OutputTokens: chunk.Usage.CompletionTokens,
				}
			}
			continue
		}

		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			textAccum.WriteString(delta.Content)

			// Buffer output that looks like it might be a JSON tool call.
			// Once we see enough to know it's not JSON, flush the buffer.
			if !jsonLikely {
				trimSoFar := strings.TrimSpace(textAccum.String())
				if len(trimSoFar) > 0 && trimSoFar[0] == '{' {
					jsonLikely = true
				}
			}

			if jsonLikely {
				// Hold back — might be a tool call we'll parse at [DONE].
				buffered.WriteString(delta.Content)
			} else {
				eventCh <- StreamEvent{
					Type:  StreamEventContentDelta,
					Delta: delta.Content,
				}
			}
		}

		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			ts, ok := activeTools[idx]
			if !ok {
				ts = &toolState{}
				activeTools[idx] = ts
			}
			if tc.ID != "" {
				ts.id = tc.ID
			}
			if tc.Function.Name != "" {
				ts.name = tc.Function.Name
			}
			ts.args.WriteString(tc.Function.Arguments)
		}

		if chunk.Choices[0].FinishReason != nil {
			if chunk.Usage != nil {
				eventCh <- StreamEvent{
					Type:         StreamEventMessageEnd,
					OutputTokens: chunk.Usage.CompletionTokens,
				}
			}
		}
	}
}
