package main

import "testing"

// --public-url beats PUBLIC_URL, so an operator can override a baked-in
// environment for one run without editing ~/.vega/env.
func TestPickPublicURL(t *testing.T) {
	cases := []struct{ flag, env, want string }{
		{"http://vega.const", "http://env.example.com", "http://vega.const"},
		{"", "http://env.example.com", "http://env.example.com"},
		{"", "", ""},
		{"  http://vega.const  ", "", "http://vega.const"},
	}
	for _, tc := range cases {
		if got := pickPublicURL(tc.flag, tc.env); got != tc.want {
			t.Errorf("pickPublicURL(%q, %q) = %q, want %q", tc.flag, tc.env, got, tc.want)
		}
	}
}
