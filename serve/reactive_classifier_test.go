package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/everydev1618/govega/events"
	"github.com/everydev1618/govega/llm"
)

type classifierLLM struct {
	content string
	err     error
}

func (f classifierLLM) Generate(context.Context, []llm.Message, []llm.ToolSchema) (*llm.LLMResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.LLMResponse{Content: f.content}, nil
}

func (f classifierLLM) GenerateStream(context.Context, []llm.Message, []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

func TestLLMClassifierSalient(t *testing.T) {
	e := events.Event{Type: "agent.completed", Data: map[string]any{"status": "ok"}}

	cases := []struct {
		name    string
		backend llm.LLM
		want    bool
	}{
		{"yes", classifierLLM{content: "YES — investor email, worth acting"}, true},
		{"no", classifierLLM{content: "NO, routine noise"}, false},
		{"error fails open", classifierLLM{err: errors.New("boom")}, true},
		{"nil backend fails open", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &llmClassifier{llm: func() llm.LLM { return tc.backend }}
			got, reason := c.Salient(t.Context(), "watcher", "do the thing", e)
			if got != tc.want {
				t.Fatalf("Salient = %v (reason %q), want %v", got, reason, tc.want)
			}
		})
	}
}
