package serve

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "no urls",
			in:   "Dashboard is built and saved to /data/workspace/dashboard.html",
			want: []string{},
		},
		{
			name: "single fly.dev url at end of sentence",
			in:   "Dashboard live at https://acme-dashboard-8b24eb.fly.dev/dashboard.html.",
			want: []string{"https://acme-dashboard-8b24eb.fly.dev/dashboard.html"},
		},
		{
			name: "url inside markdown brackets — captured cleanly",
			in:   "See [the dashboard](https://acme-dashboard-8b24eb.fly.dev/dashboard.html) for details",
			want: []string{"https://acme-dashboard-8b24eb.fly.dev/dashboard.html"},
		},
		{
			name: "dedupes repeated url",
			in:   "URL: https://x.fly.dev/a — verified at https://x.fly.dev/a (200 OK)",
			want: []string{"https://x.fly.dev/a"},
		},
		{
			name: "trailing comma and paren stripped",
			in:   "Live at https://x.fly.dev/dashboard.html, also tested via https://x.fly.dev/index.html)",
			want: []string{"https://x.fly.dev/dashboard.html", "https://x.fly.dev/index.html"},
		},
		{
			name: "multiple distinct urls",
			in:   "Dashboard https://a.fly.dev plus admin https://b.fly.dev/admin",
			want: []string{"https://a.fly.dev", "https://b.fly.dev/admin"},
		},
		{
			name: "http (insecure) also captured",
			in:   "Local mirror at http://localhost:8080/dashboard.html",
			want: []string{"http://localhost:8080/dashboard.html"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractURLs(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("extractURLs(%q)\n  got:  %#v\n  want: %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestComposeDispatchCompletePoke(t *testing.T) {
	t.Run("no urls returns the base message only", func(t *testing.T) {
		msg := composeDispatchCompletePoke("nadia", "Dashboard built but server start failed.")
		if !strings.Contains(msg, "Agent **nadia** just finished a task") {
			t.Errorf("missing base message: %q", msg)
		}
		if strings.Contains(msg, "verbatim") {
			t.Errorf("verbatim block should be absent when no URLs present: %q", msg)
		}
	})

	t.Run("single URL: included as verbatim block with exact-chars instruction", func(t *testing.T) {
		resp := `Dashboard is live at https://acme-dashboard-8b24eb.fly.dev/dashboard.html — HTTP 200 verified.`
		msg := composeDispatchCompletePoke("nadia", resp)

		// The exact URL must appear in the message.
		if !strings.Contains(msg, "https://acme-dashboard-8b24eb.fly.dev/dashboard.html") {
			t.Errorf("URL missing from poke: %q", msg)
		}
		// And we tell the orchestrator: do not retype.
		if !strings.Contains(msg, "paste these exact characters") {
			t.Errorf("verbatim instruction missing: %q", msg)
		}
	})

	t.Run("multiple URLs: each appears on its own line under one heading", func(t *testing.T) {
		resp := `Frontend at https://a.fly.dev/dashboard.html and admin at https://b.fly.dev/admin — both verified.`
		msg := composeDispatchCompletePoke("nadia", resp)
		if !strings.Contains(msg, "https://a.fly.dev/dashboard.html") || !strings.Contains(msg, "https://b.fly.dev/admin") {
			t.Errorf("both URLs should appear: %q", msg)
		}
	})
}
