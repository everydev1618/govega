package serve

import (
	"regexp"
	"strings"
)

// markdownToTelegramHTML converts the agent's markdown output into the
// subset of HTML Telegram accepts (with parse_mode=HTML). The goal isn't a
// full markdown engine — just to make agent replies render cleanly instead
// of printing literal **bold** and asterisks in a chat.
//
// Supported conversions:
//   - ```lang\n…\n```          → <pre>…</pre>
//   - `inline code`            → <code>inline code</code>
//   - **bold** / __bold__      → <b>bold</b>
//   - *italic* / _italic_      → <i>italic</i>
//   - ~~strike~~               → <s>strike</s>
//   - # / ## / ### Heading     → <b>Heading</b>
//   - [text](url)              → <a href="url">text</a>
//   - "- item" / "* item"      → "• item"
//
// HTML special chars in the raw text are escaped (&, <, >). Code spans /
// blocks are extracted first so markdown inside them doesn't get
// re-interpreted.
func markdownToTelegramHTML(s string) string {
	// 1. Extract fenced code blocks ```…``` and inline `…` first.
	type placeholder struct {
		token string
		html  string
	}
	var placeholders []placeholder
	stash := func(html string) string {
		token := "\x00PH" + itoa(len(placeholders)) + "\x00"
		placeholders = append(placeholders, placeholder{token: token, html: html})
		return token
	}

	// Fenced blocks (greedy on language tag, lazy on body)
	s = reFenced.ReplaceAllStringFunc(s, func(m string) string {
		inner := reFenced.FindStringSubmatch(m)
		body := inner[2]
		// Escape inside code blocks too.
		body = escapeHTML(body)
		return stash("<pre>" + body + "</pre>")
	})
	// Inline code
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := reInlineCode.FindStringSubmatch(m)
		return stash("<code>" + escapeHTML(inner[1]) + "</code>")
	})

	// 2. Escape HTML special characters in the rest.
	s = escapeHTML(s)

	// 3. Headings → bold, one per line. Match early so the # doesn't get
	// confused with other patterns.
	s = reHeading.ReplaceAllString(s, "<b>$1</b>")

	// 4. Bold / italic / strike.
	s = reBoldStar.ReplaceAllString(s, "<b>$1</b>")
	s = reBoldUnder.ReplaceAllString(s, "<b>$1</b>")
	// Italic regexes capture surrounding non-marker chars to avoid matching
	// inside bold (**x**) or words like under_scored. Go regexp has no
	// lookahead/lookbehind, so we put those characters back in the
	// replacement string explicitly.
	s = reItalicStar.ReplaceAllString(s, "$1<i>$2</i>$3")
	s = reItalicUnder.ReplaceAllString(s, "$1<i>$2</i>$3")
	s = reStrike.ReplaceAllString(s, "<s>$1</s>")

	// 5. Links — text is already escaped; URL needs no escape inside an
	// attribute value as long as we keep " as the quote and the URL doesn't
	// contain raw " (rare).
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := reLink.FindStringSubmatch(m)
		text, url := parts[1], parts[2]
		return `<a href="` + url + `">` + text + `</a>`
	})

	// 6. Bulleted lists — replace "- " or "* " at line start with a bullet.
	s = reBullet.ReplaceAllString(s, "$1• ")

	// 7. Restore code placeholders.
	for _, p := range placeholders {
		s = strings.ReplaceAll(s, p.token, p.html)
	}
	return s
}

func escapeHTML(s string) string {
	// & must come first so we don't double-escape entities we ourselves create.
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func itoa(i int) string {
	// Tiny strconv-free int→string. We only stash a few placeholders per msg.
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

var (
	reFenced      = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\n?(.*?)```")
	reInlineCode  = regexp.MustCompile("`([^`\n]+)`")
	reHeading     = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reBoldStar    = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reBoldUnder   = regexp.MustCompile(`__([^_\n]+)__`)
	reItalicStar  = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*([^*]|$)`)
	reItalicUnder = regexp.MustCompile(`(^|[^_])_([^_\n]+)_([^_]|$)`)
	reStrike      = regexp.MustCompile(`~~([^~\n]+)~~`)
	reLink        = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBullet      = regexp.MustCompile(`(?m)^([\t ]*)[-*]\s+`)
)
