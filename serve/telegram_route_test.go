package serve

import "testing"

func TestParseAgentPrefix(t *testing.T) {
	known := map[string]bool{
		"finance":   true,
		"iris":      true,
		"sales-rep": true,
		"a1_b2":     true,
	}
	has := func(name string) bool { return known[name] }

	cases := []struct {
		name      string
		in        string
		wantAgent string
		wantBody  string
	}{
		{"no_prefix", "hello there", "", "hello there"},
		{"simple", "!finance what's our runway?", "finance", "what's our runway?"},
		{"trailing_whitespace_only", "!finance   ", "finance", ""},
		{"unknown_agent_passes_through", "!nobody hey", "", "!nobody hey"},
		{"hyphen_and_digits", "!sales-rep ping", "sales-rep", "ping"},
		{"underscore_and_digits", "!a1_b2 ping", "a1_b2", "ping"},
		{"bang_not_at_start", "hey !finance ping", "", "hey !finance ping"},
		{"empty", "", "", ""},
		{"just_bang", "!", "", "!"},
		{"bang_with_no_name", "! finance hi", "", "! finance hi"},
		{"bang_then_punct", "!! shout", "", "!! shout"},
		{"prefix_only_no_body", "!iris", "iris", ""},
		{"newline_after_prefix", "!iris\nplease help", "iris", "please help"},
		{"tab_after_prefix", "!iris\tplease help", "iris", "please help"},
		{"case_sensitive_known", "!Finance hi", "", "!Finance hi"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAgent, gotBody := parseAgentPrefix(tc.in, has)
			if gotAgent != tc.wantAgent || gotBody != tc.wantBody {
				t.Errorf("parseAgentPrefix(%q)\n  got: (%q, %q)\n want: (%q, %q)",
					tc.in, gotAgent, gotBody, tc.wantAgent, tc.wantBody)
			}
		})
	}
}
