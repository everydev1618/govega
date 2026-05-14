package serve

import "testing"

func TestMarkdownToTelegramHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"bold", "make it **bold**", "make it <b>bold</b>"},
		{"italic_star", "kinda *slanted* yo", "kinda <i>slanted</i> yo"},
		{"italic_under", "kinda _slanted_ yo", "kinda <i>slanted</i> yo"},
		{"strikethrough", "wrong ~~old~~ new", "wrong <s>old</s> new"},
		{"inline_code", "use `npm install`", "use <code>npm install</code>"},
		{"heading", "# Title\nbody", "<b>Title</b>\nbody"},
		{"link", "see [docs](https://example.com)", `see <a href="https://example.com">docs</a>`},
		{"bullets", "- one\n- two", "• one\n• two"},
		{"html_escape", "<script>&", "&lt;script&gt;&amp;"},
		{
			name: "fenced_code",
			in:   "```\nfoo bar\n```",
			want: "<pre>foo bar\n</pre>",
		},
		{
			name: "fenced_code_with_lang",
			in:   "```go\nfmt.Println(\"hi\")\n```",
			// Quotes don't need HTML-escaping (we only escape & < >).
			want: "<pre>fmt.Println(\"hi\")\n</pre>",
		},
		{
			name: "code_block_protects_markdown",
			in:   "```\n**not bold**\n```",
			want: "<pre>**not bold**\n</pre>",
		},
		{
			name: "html_in_inline_code_is_escaped",
			in:   "`<script>`",
			want: "<code>&lt;script&gt;</code>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := markdownToTelegramHTML(tc.in)
			if got != tc.want {
				t.Errorf("markdownToTelegramHTML(%q)\n  got: %q\n want: %q", tc.in, got, tc.want)
			}
		})
	}
}
