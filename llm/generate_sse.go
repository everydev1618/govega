package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Generate fetches a completion over the streaming endpoint and reassembles it
// into a complete response.
//
// The sync endpoint bounds a request by the HTTP client's timeout, which spans
// the whole response body — so a long generation is killed rather than waited
// for, and Anthropic rejects non-streaming requests whose expected duration is
// too long outright. Streaming removes that ceiling: bytes arrive throughout,
// and the result is identical because reassembly feeds the same parseResponse
// the sync path used.

// sseBlock accumulates one content block as its deltas arrive.
type sseBlock struct {
	typ       string
	id        string
	name      string
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	partial   strings.Builder // input_json_delta fragments for tool_use
}

// raw renders the finished block as the wire JSON parseResponse expects.
func (b *sseBlock) raw() (json.RawMessage, error) {
	block := contentBlock{Type: b.typ}
	switch b.typ {
	case "text":
		block.Text = b.text.String()
	case "thinking":
		block.Thinking = b.thinking.String()
		block.Signature = b.signature.String()
	case "tool_use":
		block.ID = b.id
		block.Name = b.name
		block.Input = map[string]any{}
		if s := b.partial.String(); s != "" {
			if err := json.Unmarshal([]byte(s), &block.Input); err != nil {
				return nil, fmt.Errorf("tool input %q: %w", b.name, err)
			}
		}
	default:
		// Server-side tool blocks and anything new: keep the type so the
		// block survives, even though its payload is not modelled here.
	}
	return json.Marshal(block)
}

// reassembleSSE rebuilds an anthropicResponse from a streamed message.
//
// A stream that ends before message_stop is an error: a truncated answer must
// not be returned as if it were complete.
func reassembleSSE(reader io.Reader, fallbackModel string) (*anthropicResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxSSELineBytes)

	resp := &anthropicResponse{Model: fallbackModel}
	blocks := map[int]*sseBlock{}
	var order []int
	sawMessageStop := false

	var currentEvent string
	var currentData strings.Builder

	blockAt := func(i int) *sseBlock {
		b, ok := blocks[i]
		if !ok {
			b = &sseBlock{}
			blocks[i] = b
			order = append(order, i)
		}
		return b
	}

	handle := func(event, data string) error {
		switch event {
		case "message_start":
			var m struct {
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Role  string `json:"role"`
					Usage struct {
						InputTokens              int `json:"input_tokens"`
						OutputTokens             int `json:"output_tokens"`
						CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
						CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				return fmt.Errorf("message_start: %w", err)
			}
			resp.ID = m.Message.ID
			resp.Role = m.Message.Role
			if m.Message.Model != "" {
				resp.Model = m.Message.Model
			}
			resp.Usage.InputTokens = m.Message.Usage.InputTokens
			resp.Usage.CacheCreationInputTokens = m.Message.Usage.CacheCreationInputTokens
			resp.Usage.CacheReadInputTokens = m.Message.Usage.CacheReadInputTokens
			// Some responses report output tokens only in message_delta.
			if m.Message.Usage.OutputTokens > 0 {
				resp.Usage.OutputTokens = m.Message.Usage.OutputTokens
			}

		case "content_block_start":
			var e struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type      string `json:"type"`
					ID        string `json:"id"`
					Name      string `json:"name"`
					Text      string `json:"text"`
					Thinking  string `json:"thinking"`
					Signature string `json:"signature"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				return fmt.Errorf("content_block_start: %w", err)
			}
			b := blockAt(e.Index)
			b.typ = e.ContentBlock.Type
			b.id = e.ContentBlock.ID
			b.name = e.ContentBlock.Name
			b.text.WriteString(e.ContentBlock.Text)
			b.thinking.WriteString(e.ContentBlock.Thinking)
			b.signature.WriteString(e.ContentBlock.Signature)

		case "content_block_delta":
			var e struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				return fmt.Errorf("content_block_delta: %w", err)
			}
			b := blockAt(e.Index)
			switch e.Delta.Type {
			case "text_delta":
				b.text.WriteString(e.Delta.Text)
			case "input_json_delta":
				b.partial.WriteString(e.Delta.PartialJSON)
			case "thinking_delta":
				b.thinking.WriteString(e.Delta.Thinking)
			case "signature_delta":
				b.signature.WriteString(e.Delta.Signature)
			}

		case "message_delta":
			var e struct {
				Delta struct {
					StopReason   string `json:"stop_reason"`
					StopSequence string `json:"stop_sequence"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				return fmt.Errorf("message_delta: %w", err)
			}
			if e.Delta.StopReason != "" {
				resp.StopReason = e.Delta.StopReason
			}
			if e.Delta.StopSequence != "" {
				resp.StopSequence = e.Delta.StopSequence
			}
			if e.Usage.OutputTokens > 0 {
				resp.Usage.OutputTokens = e.Usage.OutputTokens
			}

		case "message_stop":
			sawMessageStop = true

		case "error":
			return fmt.Errorf("stream error event: %s", data)
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			currentData.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "" && currentEvent != "":
			if err := handle(currentEvent, currentData.String()); err != nil {
				return nil, err
			}
			currentEvent = ""
			currentData.Reset()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read failed: %w", err)
	}
	if !sawMessageStop {
		return nil, fmt.Errorf("stream truncated: connection closed before message_stop")
	}

	// Blocks render in the order the stream opened them.
	for _, i := range order {
		rawBlock, err := blocks[i].raw()
		if err != nil {
			return nil, err
		}
		resp.Content = append(resp.Content, rawBlock)
	}
	return resp, nil
}

// doStreamRequest performs a streaming request and reassembles the result.
//
// Retries mirror doRequest, but only up to the point the response body starts
// arriving: once a 200 stream is open, a failure mid-body is reported rather
// than silently restarted.
func (a *AnthropicLLM) doStreamRequest(ctx context.Context, req *anthropicRequest) (*anthropicResponse, error) {
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

		// streamClient has no overall Timeout: http.Client.Timeout spans the
		// whole body, so a bound there truncates a working stream.
		client := a.streamClient
		if client == nil {
			client = a.httpClient
		}

		httpResp, err := client.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < maxRetries {
				wait := retryAfterDelay(nil, attempt, a.retryBase)
				slog.Warn("API request failed (stream), retrying", "error", err, "attempt", attempt+1, "wait", wait)
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, fmt.Errorf("http request: %w", err)
		}

		if httpResp.StatusCode == http.StatusOK {
			resp, err := reassembleSSE(httpResp.Body, req.Model)
			httpResp.Body.Close()
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, err
			}
			return resp, nil
		}

		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()

		if isRetryableStatus(httpResp.StatusCode) && attempt < maxRetries {
			wait := retryAfterDelay(httpResp, attempt, a.retryBase)
			slog.Warn("API transient error (stream), retrying", "status", httpResp.StatusCode, "attempt", attempt+1, "wait", wait)
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
			"stream", true,
			"thinking", req.Thinking != nil,
			"tools", len(req.Tools),
		)
		return nil, fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("max retries exceeded")
}
