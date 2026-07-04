package serve

import "testing"

// publicBaseURL decides the base URL agents use to report deliverable links
// (…/workspace/…). Operators can set it via Config.PublicURL; embedders that
// don't wire the config field still get the PUBLIC_URL env var honored, so a
// hosted instance never hands users a dead localhost link.
func TestPublicBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		cfgURL  string
		envURL  string
		port    string
		want    string
	}{
		{"config wins", "https://cfg.example.com/", "https://env.example.com", "3001", "https://cfg.example.com"},
		{"env fallback when config empty", "", "https://et.v39a.com/", "3001", "https://et.v39a.com"},
		{"localhost when both empty", "", "", "3001", "http://localhost:3001"},
		{"trailing slashes trimmed", "https://cfg.example.com///", "", "8080", "https://cfg.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := publicBaseURL(tc.cfgURL, tc.envURL, tc.port)
			if got != tc.want {
				t.Errorf("publicBaseURL(%q, %q, %q) = %q, want %q", tc.cfgURL, tc.envURL, tc.port, got, tc.want)
			}
		})
	}
}
