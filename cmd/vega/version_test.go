package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	buildInfo := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}

	tests := []struct {
		name     string
		ldflags  string
		info     *debug.BuildInfo
		ok       bool
		expected string
	}{
		{
			// GoReleaser stamps -X main.version; it always wins.
			name:     "ldflags version wins over build info",
			ldflags:  "0.9.3",
			info:     buildInfo("v0.9.9"),
			ok:       true,
			expected: "0.9.3",
		},
		{
			// `go install ...@latest` — the module proxy version is the truth.
			name:     "falls back to module version",
			ldflags:  "dev",
			info:     buildInfo("v0.9.3"),
			ok:       true,
			expected: "v0.9.3",
		},
		{
			// `go build` in a working tree reports this sentinel, not a version.
			name:     "devel sentinel stays dev",
			ldflags:  "dev",
			info:     buildInfo("(devel)"),
			ok:       true,
			expected: "dev",
		},
		{
			name:     "empty module version stays dev",
			ldflags:  "dev",
			info:     buildInfo(""),
			ok:       true,
			expected: "dev",
		},
		{
			name:     "no build info stays dev",
			ldflags:  "dev",
			info:     nil,
			ok:       false,
			expected: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersion(tt.ldflags, tt.info, tt.ok)
			if got != tt.expected {
				t.Errorf("resolveVersion(%q, %v, %v) = %q, want %q",
					tt.ldflags, tt.info, tt.ok, got, tt.expected)
			}
		})
	}
}
