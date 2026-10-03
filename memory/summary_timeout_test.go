package memory

import (
	"testing"
	"time"
)

// TestSummaryTimeout_Default pins the bound on the summarisation call that
// compacts a context window.
//
// It was 30 seconds, which is a hosted-API assumption: summarising a long
// transcript means prefilling a long prompt, and on a self-hosted model that
// alone can run for minutes — a 27B on the local fleet measured ~470s of
// prefill on a 121k-character prompt. Blowing the bound does not just lose a
// summary, it fails the compaction that was supposed to stop the window
// growing, so the one mechanism for long conversations breaks exactly when a
// conversation gets long.
func TestSummaryTimeout_Default(t *testing.T) {
	t.Setenv("VEGA_SUMMARY_TIMEOUT", "")
	if got := summaryTimeout(); got != DefaultSummaryTimeout {
		t.Fatalf("summaryTimeout() = %v, want %v", got, DefaultSummaryTimeout)
	}
	if DefaultSummaryTimeout < 10*time.Minute {
		t.Fatalf("DefaultSummaryTimeout = %v; too tight for a self-hosted prefill", DefaultSummaryTimeout)
	}
}

func TestSummaryTimeout_EnvOverride(t *testing.T) {
	t.Setenv("VEGA_SUMMARY_TIMEOUT", "45s")
	if got := summaryTimeout(); got != 45*time.Second {
		t.Fatalf("summaryTimeout() = %v, want 45s", got)
	}
}

// TestSummaryTimeout_RejectsUnusableValues: a typo must not produce an
// already-expired context, which fails identically to a dead backend.
func TestSummaryTimeout_RejectsUnusableValues(t *testing.T) {
	for _, v := range []string{"soon", "0", "-5m"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("VEGA_SUMMARY_TIMEOUT", v)
			if got := summaryTimeout(); got != DefaultSummaryTimeout {
				t.Fatalf("summaryTimeout() = %v for %q, want %v", got, v, DefaultSummaryTimeout)
			}
		})
	}
}
