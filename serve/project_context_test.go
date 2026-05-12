package serve

import (
	"context"
	"strings"
	"testing"
)

func TestBuildExtraSystemEmpty(t *testing.T) {
	got := buildExtraSystem("", "", "")
	if got != "" {
		t.Errorf("buildExtraSystem(empty) = %q, want empty", got)
	}
}

func TestBuildExtraSystemOrdering(t *testing.T) {
	got := buildExtraSystem("MEM", "PROJ", "COMP")
	// Company first, then memory, then project.
	if !strings.Contains(got, "COMP") || !strings.Contains(got, "MEM") || !strings.Contains(got, "PROJ") {
		t.Fatalf("buildExtraSystem missing parts: %q", got)
	}
	idxCompany := strings.Index(got, "COMP")
	idxMem := strings.Index(got, "MEM")
	idxProj := strings.Index(got, "PROJ")
	if !(idxCompany < idxMem && idxMem < idxProj) {
		t.Errorf("order wrong; COMP=%d MEM=%d PROJ=%d", idxCompany, idxMem, idxProj)
	}
}

func TestComposeExtraSystemNilProvider(t *testing.T) {
	s := &Server{}
	got := s.composeExtraSystem(context.Background(), "guide:user_a:b", "guide", "user_a", "MEM", "PROJ", "COMP")
	want := buildExtraSystem("MEM", "PROJ", "COMP")
	if got != want {
		t.Errorf("nil provider should match plain buildExtraSystem;\n got: %q\nwant: %q", got, want)
	}
}

func TestComposeExtraSystemProviderAppends(t *testing.T) {
	var gotAgent, gotBase, gotUser string
	s := &Server{
		cfg: Config{
			ExtraSystemProvider: func(_ context.Context, agentName, baseAgent, userID string) string {
				gotAgent = agentName
				gotBase = baseAgent
				gotUser = userID
				return "NORM-BODY"
			},
		},
	}
	got := s.composeExtraSystem(context.Background(), "guide:user_a:my-book", "guide", "user_a", "MEM", "", "")
	if !strings.Contains(got, "MEM") {
		t.Errorf("composed result missing base extras: %q", got)
	}
	if !strings.Contains(got, "NORM-BODY") {
		t.Errorf("composed result missing provider content: %q", got)
	}
	if gotAgent != "guide:user_a:my-book" {
		t.Errorf("provider got agentName = %q", gotAgent)
	}
	if gotBase != "guide" {
		t.Errorf("provider got baseAgent = %q", gotBase)
	}
	if gotUser != "user_a" {
		t.Errorf("provider got userID = %q", gotUser)
	}
}

func TestComposeExtraSystemProviderEmpty(t *testing.T) {
	s := &Server{
		cfg: Config{
			ExtraSystemProvider: func(_ context.Context, _, _, _ string) string { return "" },
		},
	}
	got := s.composeExtraSystem(context.Background(), "guide", "guide", "user_a", "MEM", "", "")
	// Empty provider output must not add stray separators.
	if strings.HasSuffix(got, "\n\n") {
		t.Errorf("empty provider should not append separator; got %q", got)
	}
	if !strings.Contains(got, "MEM") {
		t.Errorf("base extras lost: %q", got)
	}
}

func TestComposeExtraSystemBaseEmpty(t *testing.T) {
	s := &Server{
		cfg: Config{
			ExtraSystemProvider: func(_ context.Context, _, _, _ string) string { return "NORM" },
		},
	}
	got := s.composeExtraSystem(context.Background(), "guide", "guide", "user_a", "", "", "")
	if got != "NORM" {
		t.Errorf("provider-only path should return provider content verbatim; got %q", got)
	}
}
