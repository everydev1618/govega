package tools

import (
	"regexp"
	"strings"
)

// markdownToSlackMrkdwn converts standard markdown formatting to Slack's mrkdwn format.
func markdownToSlackMrkdwn(text string) string {
	// Split into code-fenced blocks vs prose so we don't mangle code.
	parts := strings.Split(text, "```")
	for i := 0; i < len(parts); i += 2 {
		// Even-indexed parts are outside code fences.
		parts[i] = convertProse(parts[i])
	}
	return strings.Join(parts, "```")
}

var (
	// Links: [text](url) → <url|text>
	reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// Bold: **text** → *text*
	reBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Italic: *text* (not **) → _text_
	reItalic = regexp.MustCompile(`(?:^|[^*])\*([^*]+?)\*(?:[^*]|$)`)
	// Strikethrough: ~~text~~ → ~text~
	reStrike = regexp.MustCompile(`~~(.+?)~~`)
	// Headings: # Heading → *Heading* (lines starting with 1-6 hashes)
	reHeading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
)

func convertProse(s string) string {
	// Order: links first, then italic (before bold so ** isn't partially matched),
	// then bold, strike, headings.
	s = reLink.ReplaceAllString(s, "<$2|$1>")
	s = convertItalic(s)
	s = reBold.ReplaceAllString(s, "*$1*")
	s = reStrike.ReplaceAllString(s, "~$1~")
	s = reHeading.ReplaceAllString(s, "*$1*")
	return s
}

// convertItalic converts *text* → _text_ without matching **bold**.
func convertItalic(s string) string {
	for {
		loc := reItalic.FindStringIndex(s)
		if loc == nil {
			break
		}
		match := s[loc[0]:loc[1]]
		firstStar := strings.Index(match, "*")
		lastStar := strings.LastIndex(match, "*")
		if firstStar == lastStar {
			break
		}
		inner := match[firstStar+1 : lastStar]
		replacement := match[:firstStar] + "_" + inner + "_" + match[lastStar+1:]
		s = s[:loc[0]] + replacement + s[loc[1]:]
	}
	return s
}

// convertSlackArgs walks an args map and converts known text fields from markdown to Slack mrkdwn.
func convertSlackArgs(args map[string]any) {
	textFields := []string{"text", "message", "content"}
	for _, key := range textFields {
		if v, ok := args[key].(string); ok {
			args[key] = markdownToSlackMrkdwn(v)
		}
	}
}
