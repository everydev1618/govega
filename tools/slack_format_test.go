package tools

import "testing"

func TestMarkdownToSlackMrkdwn(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bold",
			in:   "This is **bold** text",
			want: "This is *bold* text",
		},
		{
			name: "italic",
			in:   "This is *italic* text",
			want: "This is _italic_ text",
		},
		{
			name: "strikethrough",
			in:   "This is ~~struck~~ text",
			want: "This is ~struck~ text",
		},
		{
			name: "link",
			in:   "Click [here](https://example.com) now",
			want: "Click <https://example.com|here> now",
		},
		{
			name: "heading",
			in:   "# My Heading",
			want: "*My Heading*",
		},
		{
			name: "heading level 2",
			in:   "## Sub Heading",
			want: "*Sub Heading*",
		},
		{
			name: "inline code unchanged",
			in:   "Use `fmt.Println` here",
			want: "Use `fmt.Println` here",
		},
		{
			name: "code block unchanged",
			in:   "Before\n```\ncode here\n```\nAfter **bold**",
			want: "Before\n```\ncode here\n```\nAfter *bold*",
		},
		{
			name: "bold and italic combined",
			in:   "**bold** and *italic*",
			want: "*bold* and _italic_",
		},
		{
			name: "link with description",
			in:   "See [the docs](https://docs.example.com/path) for details",
			want: "See <https://docs.example.com/path|the docs> for details",
		},
		{
			name: "multiple conversions",
			in:   "# Title\n\n**Important**: Visit [site](https://x.com) and ~~ignore~~ this",
			want: "*Title*\n\n*Important*: Visit <https://x.com|site> and ~ignore~ this",
		},
		{
			name: "plain text unchanged",
			in:   "Just plain text here",
			want: "Just plain text here",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdownToSlackMrkdwn(tt.in)
			if got != tt.want {
				t.Errorf("markdownToSlackMrkdwn(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConvertSlackArgs(t *testing.T) {
	args := map[string]any{
		"text":    "**bold** text",
		"message": "Visit [here](https://x.com)",
		"content": "~~strike~~",
		"channel": "#general",
		"count":   42,
	}

	convertSlackArgs(args)

	if args["text"] != "*bold* text" {
		t.Errorf("text field: got %q", args["text"])
	}
	if args["message"] != "Visit <https://x.com|here>" {
		t.Errorf("message field: got %q", args["message"])
	}
	if args["content"] != "~strike~" {
		t.Errorf("content field: got %q", args["content"])
	}
	if args["channel"] != "#general" {
		t.Errorf("channel field should be unchanged: got %q", args["channel"])
	}
	if args["count"] != 42 {
		t.Errorf("count field should be unchanged: got %v", args["count"])
	}
}
