package dsl

import "strings"

// TruncatePreview shortens s to at most max runes for use in a preview that is
// later rendered as (or embedded inside) Markdown, then appends suffix if any
// truncation occurred. It fixes the byte-slice anti-pattern that scattered the
// codebase (content[:150], preview[:300], …): a raw byte slice can split a
// multibyte UTF-8 rune in half and cut a Markdown token mid-span, so an
// unterminated **bold** or `code` span renders as literal asterisks/backticks
// downstream.
//
// It does three things a byte slice does not:
//   - truncates on a rune boundary, never emitting invalid UTF-8;
//   - backs off to the last word boundary near the limit for a clean cut;
//   - closes a still-open inline code or bold span before the suffix, so the
//     preview always renders as balanced Markdown.
//
// Balancing is deliberately limited to the two tokens that caused real
// breakage (inline `code` and **bold**); single-* / _ italics and links are
// left as-is. Inside an open code span all other tokens are literal, so bold
// is only balanced when the code span is already balanced.
func TruncatePreview(s string, max int, suffix string) string {
	if max <= 0 {
		return suffix
	}

	// Fast path: count runes while finding the byte offset of rune `max`.
	// Bail early the moment we know s fits — avoids scanning huge strings.
	cut := -1
	n := 0
	for i := range s {
		if n == max {
			cut = i
			break
		}
		n++
	}
	if cut < 0 {
		return s // s has <= max runes; nothing to truncate
	}

	stem := s[:cut]

	// Word-boundary backoff: prefer breaking at whitespace, but only if that
	// whitespace is within the last ~40% of the budget — otherwise a long
	// unbroken token right after an early space would be gutted to almost
	// nothing.
	floor := max * 6 / 10
	if sp := strings.LastIndexAny(stem, " \t\n"); sp >= 0 {
		if runesBefore(stem, sp) >= floor {
			stem = stem[:sp]
		}
	}
	stem = strings.TrimRight(stem, " \t\n")

	// Balance the inline Markdown tokens that break rendering when left open.
	if strings.Count(stem, "`")%2 == 1 {
		stem += "`"
	} else if strings.Count(stem, "**")%2 == 1 {
		stem += "**"
	}

	return stem + suffix
}

// runesBefore returns the number of runes in s that precede byte offset off.
func runesBefore(s string, off int) int {
	n := 0
	for i := range s {
		if i >= off {
			break
		}
		n++
	}
	return n
}
