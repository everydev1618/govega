package llm

import (
	"testing"
	"time"
)

func TestParseResponseStopReasons(t *testing.T) {
	tests := []struct {
		apiReason  string
		wantStop   StopReason
	}{
		{"end_turn", StopReasonEnd},
		{"tool_use", StopReasonToolUse},
		{"max_tokens", StopReasonLength},
		{"stop_sequence", StopReasonStop},
		{"pause_turn", StopReasonPause},
		{"refusal", StopReasonRefusal},
		{"model_context_window_exceeded", StopReasonContextExceeded},
	}

	a := &AnthropicLLM{}
	for _, tt := range tests {
		t.Run(tt.apiReason, func(t *testing.T) {
			resp := &anthropicResponse{
				StopReason: tt.apiReason,
				Model:      "claude-opus-4-7",
			}
			out, err := a.parseResponse(resp, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if out.StopReason != tt.wantStop {
				t.Errorf("StopReason for %q = %q, want %q", tt.apiReason, out.StopReason, tt.wantStop)
			}
		})
	}
}
