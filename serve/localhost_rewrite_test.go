package serve

import "testing"

// Belt and braces for the prompt layer: even with every tool now minting
// correct links, a model can still type a localhost URL from memory. Any
// reference to *our own* port is rewritten to the public base on the way out.
// Other local ports are left alone — an agent telling the user about a dev
// server on :3000 is making a different, legitimate statement.
func TestRewriteLocalhostURLs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		base string
		port string
		want string
	}{
		{
			name: "our own port rewritten",
			in:   "the draft is final at: http://localhost:8822/workspace/moneyball-agents.md",
			base: "http://vega.const", port: "8822",
			want: "the draft is final at: http://vega.const/workspace/moneyball-agents.md",
		},
		{
			name: "127.0.0.1 form rewritten",
			in:   "open http://127.0.0.1:8822/workspace/a.html",
			base: "http://vega.const", port: "8822",
			want: "open http://vega.const/workspace/a.html",
		},
		{
			name: "ipv6 loopback form rewritten",
			in:   "open http://[::1]:8822/workspace/a.html",
			base: "http://vega.const", port: "8822",
			want: "open http://vega.const/workspace/a.html",
		},
		{
			name: "markdown link rewritten",
			in:   "see [the draft](http://localhost:8822/workspace/a.md)",
			base: "https://vega.example.com", port: "8822",
			want: "see [the draft](https://vega.example.com/workspace/a.md)",
		},
		{
			name: "multiple occurrences",
			in:   "http://localhost:8822/workspace/a.md and http://localhost:8822/workspace/b.md",
			base: "http://vega.const", port: "8822",
			want: "http://vega.const/workspace/a.md and http://vega.const/workspace/b.md",
		},
		{
			name: "other ports untouched",
			in:   "your dev server is on http://localhost:3000/",
			base: "http://vega.const", port: "8822",
			want: "your dev server is on http://localhost:3000/",
		},
		{
			name: "no-op when base is itself localhost",
			in:   "http://localhost:8822/workspace/a.md",
			base: "http://localhost:8822", port: "8822",
			want: "http://localhost:8822/workspace/a.md",
		},
		{
			name: "no-op with empty base",
			in:   "http://localhost:8822/workspace/a.md",
			base: "", port: "8822",
			want: "http://localhost:8822/workspace/a.md",
		},
		{
			name: "https base with https localhost reference",
			in:   "https://localhost:8822/workspace/a.md",
			base: "https://vega.example.com", port: "8822",
			want: "https://vega.example.com/workspace/a.md",
		},
		{
			name: "longer port not confused with ours",
			in:   "http://localhost:88220/x",
			base: "http://vega.const", port: "8822",
			want: "http://localhost:88220/x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteLocalhostURLs(tc.in, tc.base, tc.port); got != tc.want {
				t.Errorf("rewriteLocalhostURLs(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}
