package llm

import "os"

// New creates an LLM backend based on environment configuration.
// If OPENAI_BASE_URL is set, it returns an OpenAI-compatible client
// (works with LiteLLM, Ollama, vLLM, etc). Otherwise it returns
// an Anthropic client.
func New() LLM {
	if os.Getenv("OPENAI_BASE_URL") != "" {
		return NewOpenAI()
	}
	return NewAnthropic()
}
