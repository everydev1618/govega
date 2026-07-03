package serve

import (
	"context"
	"strings"
	"testing"
)

func TestBuildExtraSystemEmpty(t *testing.T) {
	got := buildExtraSystem("", "", "", "")
	if got != "" {
		t.Errorf("buildExtraSystem(empty) = %q, want empty", got)
	}
}

func TestBuildExtraSystemOrdering(t *testing.T) {
	got := buildExtraSystem("SURF", "MEM", "PROJ", "COMP")
	// Surface first, then company, then memory, then project.
	if !strings.Contains(got, "SURF") || !strings.Contains(got, "COMP") || !strings.Contains(got, "MEM") || !strings.Contains(got, "PROJ") {
		t.Fatalf("buildExtraSystem missing parts: %q", got)
	}
	idxSurf := strings.Index(got, "SURF")
	idxCompany := strings.Index(got, "COMP")
	idxMem := strings.Index(got, "MEM")
	idxProj := strings.Index(got, "PROJ")
	if !(idxSurf < idxCompany && idxCompany < idxMem && idxMem < idxProj) {
		t.Errorf("order wrong; SURF=%d COMP=%d MEM=%d PROJ=%d", idxSurf, idxCompany, idxMem, idxProj)
	}
}

func TestBuildExtraSystemSurfaceFirst(t *testing.T) {
	// Surface text appears at the very top of the block, ahead of company.
	got := buildExtraSystem("SURFACE-NOTE", "", "", "COMP")
	if !strings.HasPrefix(got, "SURFACE-NOTE") {
		t.Errorf("surface text should lead the extra-system block; got %q", got)
	}
}

func TestBuildExtraSystemSurfaceOmittedWhenEmpty(t *testing.T) {
	// An empty surface must not introduce a leading separator.
	got := buildExtraSystem("", "MEM", "", "")
	if got != "MEM" {
		t.Errorf("empty surface should be omitted cleanly; got %q", got)
	}
}

func TestSurfaceContextWeb(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	got := surfaceContext(surfaceWeb)
	if !strings.Contains(strings.ToLower(got), "web dashboard") {
		t.Errorf("web surface should mention the web dashboard; got %q", got)
	}
}

func TestSurfaceContextDiscordIncludesURL(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://vega.example.com")
	got := surfaceContext(surfaceDiscord)
	if !strings.Contains(strings.ToLower(got), "discord") {
		t.Errorf("discord surface should mention Discord; got %q", got)
	}
	if !strings.Contains(got, "https://vega.example.com") {
		t.Errorf("discord surface should include PUBLIC_URL when set; got %q", got)
	}
}

func TestSurfaceContextTelegram(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	got := surfaceContext(surfaceTelegram)
	if !strings.Contains(strings.ToLower(got), "telegram") {
		t.Errorf("telegram surface should mention Telegram; got %q", got)
	}
}

func TestSurfaceContextUnknownEmpty(t *testing.T) {
	if got := surfaceContext(""); got != "" {
		t.Errorf("empty/unknown surface should yield empty string; got %q", got)
	}
}

func TestComposeExtraSystemNilProvider(t *testing.T) {
	s := &Server{}
	got := s.composeExtraSystem(context.Background(), "guide:user_a:b", "guide", "user_a", "SURF", "MEM", "PROJ", "COMP")
	want := buildExtraSystem("SURF", "MEM", "PROJ", "COMP")
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
	got := s.composeExtraSystem(context.Background(), "guide:user_a:my-book", "guide", "user_a", "", "MEM", "", "")
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
	got := s.composeExtraSystem(context.Background(), "guide", "guide", "user_a", "", "MEM", "", "")
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
	got := s.composeExtraSystem(context.Background(), "guide", "guide", "user_a", "", "", "", "")
	if got != "NORM" {
		t.Errorf("provider-only path should return provider content verbatim; got %q", got)
	}
}
