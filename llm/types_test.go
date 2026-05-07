package llm

import (
	"math"
	"testing"
)

func TestCalculateCostCurrentModels(t *testing.T) {
	tests := []struct {
		model        string
		inputPer1M   float64
		outputPer1M  float64
	}{
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

func TestCalculateCostCacheTokens(t *testing.T) {
	// Verify cache pricing math: writes at 1.25×, reads at 0.10× of input price.
	// claude-sonnet-4-6 input is $3/1M.
	got := CalculateCost("claude-sonnet-4-6", 0, 0, 1_000_000, 1_000_000)
	want := 3.00*1.25 + 3.00*0.10 // 3.75 + 0.30 = 4.05
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("cache cost = %.4f, want %.4f", got, want)
	}
}
