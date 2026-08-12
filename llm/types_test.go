package llm

import (
	"bytes"
	"log/slog"
	"math"
	"strings"
	"testing"
)

func TestCalculateCostCurrentModels(t *testing.T) {
	tests := []struct {
		model       string
		inputPer1M  float64
		outputPer1M float64
	}{
		{"claude-fable-5", 10.00, 50.00},
		{"claude-opus-5", 5.00, 25.00},
		{"claude-sonnet-5", 3.00, 15.00},
		{"claude-opus-4-8", 5.00, 25.00},
		{"claude-opus-4-7", 5.00, 25.00},
		{"claude-opus-4-6", 5.00, 25.00},
		{"claude-opus-4-5", 5.00, 25.00},
		{"claude-sonnet-4-6", 3.00, 15.00},
		{"claude-sonnet-4-5", 3.00, 15.00},
		{"claude-haiku-4-5", 1.00, 5.00},
		{"claude-haiku-4-5-20251001", 1.00, 5.00},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			// 1M input, 1M output → input price + output price
			got := CalculateCost(tt.model, 1_000_000, 1_000_000, 0, 0)
			want := tt.inputPer1M + tt.outputPer1M
			if math.Abs(got-want) > 0.0001 {
				t.Errorf("CalculateCost(%q, 1M, 1M, 0, 0) = %.4f, want %.4f",
					tt.model, got, want)
			}
		})
	}
}

func TestCalculateCostUnknownModelFallsBack(t *testing.T) {
	// Unknown model should still return a non-zero cost via fallback.
	got := CalculateCost("not-a-real-model", 1_000_000, 0, 0, 0)
	if got <= 0 {
		t.Errorf("expected non-zero fallback cost, got %.4f", got)
	}
}

// TestCalculateCostUnknownModelWarnsLoudly verifies an unknown model logs a
// warning (instead of silently billing at stale fallback rates) and that the
// fallback never under-prices relative to the most expensive known model.
func TestCalculateCostUnknownModelWarnsLoudly(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	got := CalculateCost("mystery-model-9000", 1_000_000, 1_000_000, 0, 0)

	if !strings.Contains(buf.String(), "mystery-model-9000") {
		t.Errorf("expected a warning naming the unknown model, log output: %q", buf.String())
	}
	// Fallback must be conservative: at least Fable 5 rates ($10 + $50),
	// so budgets over-estimate rather than under-estimate unknown models.
	if want := 60.0; got < want-0.0001 {
		t.Errorf("unknown-model cost = %.4f, want >= %.4f (conservative fallback)", got, want)
	}
}

// TestCapabilitiesCurrentModels verifies the top current models are present
// with the right feature gates — a missing entry silently disables adaptive
// thinking and caps output for that model.
func TestCapabilitiesCurrentModels(t *testing.T) {
	tests := []struct {
		model string
		want  ModelCapabilities
	}{
		// Fable 5: thinking always on (adaptive accepted), effort supported,
		// sampling params removed, structured outputs, 128K output.
		{"claude-fable-5", ModelCapabilities{AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000}},
		// Opus 5: thinking on by default (adaptive accepted), sampling
		// params removed, full effort ladder, 128K output.
		{"claude-opus-5", ModelCapabilities{AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000}},
		// Sonnet 5: adaptive on by default, non-default sampling rejected.
		{"claude-sonnet-5", ModelCapabilities{AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000}},
		// Opus 4.8: same request surface as 4.7.
		{"claude-opus-4-8", ModelCapabilities{AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000}},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := CapabilitiesFor(tt.model); got != tt.want {
				t.Errorf("CapabilitiesFor(%q) = %+v, want %+v", tt.model, got, tt.want)
			}
		})
	}
}

// TestCapabilitiesUnknownModelWarns verifies an unknown model logs a warning
// so misconfigured model IDs surface instead of silently running degraded.
func TestCapabilitiesUnknownModelWarns(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	_ = CapabilitiesFor("mystery-model-9001")

	if !strings.Contains(buf.String(), "mystery-model-9001") {
		t.Errorf("expected a warning naming the unknown model, log output: %q", buf.String())
	}
}

func TestCalculateCostCacheTokens(t *testing.T) {
	// Verify cache pricing math: writes at 1.25×, reads at 0.10× of input price.
	// claude-sonnet-4-6 input is $3/1M.
	got := CalculateCost("claude-sonnet-4-6", 0, 0, 1_000_000, 1_000_000)
	want := 3.00*1.25 + 3.00*0.10 // 3.75 + 0.30 = 4.05
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("cache cost = %.4f, want %.4f", got, want)
	}
}
