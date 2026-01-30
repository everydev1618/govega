// Package llm provides LLM backend implementations for the vega package.
//
// # Anthropic Backend
//
// The primary backend is Anthropic's Claude API:
//
//	llm := llm.NewAnthropic()  // Uses ANTHROPIC_API_KEY env var
//
//	// Or with custom API key
//	llm := llm.NewAnthropic(llm.WithAPIKey("sk-..."))
//
//	// Or with custom model
//	llm := llm.NewAnthropic(llm.WithModel("claude-opus-4-20250514"))
//
// # Using with Orchestrator
//
// Configure the orchestrator to use the LLM:
//
//	llm := llm.NewAnthropic()
//	orch := vega.NewOrchestrator(vega.WithLLM(llm))
//
// # Streaming
//
// The Anthropic backend supports streaming responses:
//
//	stream, err := proc.SendStream(ctx, "Tell me a story")
//	for chunk := range stream.Chunks() {
//	    fmt.Print(chunk)
//	}
//
// # Tool Support
//
// Tools are automatically converted to the Anthropic tool format:
//
//	tools := vega.NewTools()
//	tools.Register("search", searchFunc)
//
//	agent := vega.Agent{
//	    Tools: tools,
//	    // ...
//	}
//
// When the model decides to use a tool, the tool is executed and the result
// is sent back automatically in a multi-turn conversation.
//
// # Rate Limiting
//
// The Anthropic API has rate limits. Configure rate limiting on the orchestrator:
//
//	orch := vega.NewOrchestrator(
//	    vega.WithLLM(llm),
//	    vega.WithRateLimits(map[string]vega.RateLimitConfig{
//	        "claude-sonnet-4-20250514": {
//	            RequestsPerMinute: 60,
//	            TokensPerMinute:   100000,
//	        },
//	    }),
//	)
//
// # Implementing Custom Backends
//
// To implement a custom LLM backend, implement the vega.LLM interface:
//
//	type LLM interface {
//	    Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error)
//	    GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error)
//	}
package llm
