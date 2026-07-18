package dsl

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncatePreview(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		max    int
		suffix string
		want   string
	}{
		{
			name:   "short string returned unchanged, no suffix",
			in:     "all good here",
			max:    150,
			suffix: "…",
			want:   "all good here",
		},
		{
			name:   "exact length returned unchanged",
			in:     "abcde",
			max:    5,
			suffix: "…",
			want:   "abcde",
		},
		{
			name:   "closes a dangling bold span so it never renders as literal asterisks",
			in:     "**bold text that keeps going and going past the limit",
			max:    15,
			suffix: "…",
			// First 15 runes = "**bold text tha"; word-boundary backoff to the
			// space at rune 11 → "**bold text"; the open "**" is then closed
			// before the suffix so it never renders as literal asterisks.
			want: "**bold text**…",
		},
		{
			name:   "closes a dangling inline code span",
			in:     "call `post_to_channel and then keep going for a while longer here",
			max:    21,
			suffix: "…",
			// First 21 runes = "call `post_to_channel"; the lone space at rune 4
			// is before the word-boundary floor, so no backoff; the open code
			// span is closed before the suffix.
			want: "call `post_to_channel`…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncatePreview(tt.in, tt.max, tt.suffix)
			if got != tt.want {
				t.Errorf("TruncatePreview(%q, %d, %q)\n  got:  %q\n  want: %q", tt.in, tt.max, tt.suffix, got, tt.want)
			}
		})
	}
}

// TestTruncatePreviewNeverSplitsRunes pins the core bug: a byte slice at an
// arbitrary offset can cut a multibyte UTF-8 rune in half, emitting invalid
// UTF-8. Truncation must operate on rune boundaries.
func TestTruncatePreviewNeverSplitsRunes(t *testing.T) {
	// 20 emoji = 80 bytes; any byte-slice at 150>80 would be fine, so build a
	// long multibyte string and truncate below its rune count.
	long := strings.Repeat("🎸", 50) // 50 runes, 200 bytes
	got := TruncatePreview(long, 10, "…")
	if !utf8.ValidString(got) {
		t.Fatalf("TruncatePreview produced invalid UTF-8: %q", got)
	}
	// The stem (before the suffix) must be whole runes only.
	stem := strings.TrimSuffix(got, "…")
	if strings.ContainsRune(stem, utf8.RuneError) {
		t.Fatalf("TruncatePreview stem contains RuneError (split rune): %q", stem)
	}
	if n := utf8.RuneCountInString(stem); n > 10 {
		t.Fatalf("stem has %d runes, want <= 10", n)
	}
}
