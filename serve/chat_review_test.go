package serve

import (
	"context"
	"testing"
)

// ChatResponseReviewer lets an embedding product screen a non-streaming chat
// reply before it reaches the user (e.g. a disclosure policy on a guest-facing
// agent). reviewChatResponse is the thin apply-or-passthrough seam.
func TestReviewChatResponse_NilReviewerPassesThrough(t *testing.T) {
	s := &Server{cfg: Config{}}
	got := s.reviewChatResponse(context.Background(), "concierge", "concierge", "guest", "hello")
	if got != "hello" {
		t.Errorf("nil reviewer must pass response through unchanged; got %q", got)
	}
}

func TestReviewChatResponse_ReviewerRewrites(t *testing.T) {
	var gotAgent, gotBase, gotUser, gotResp string
	s := &Server{cfg: Config{
		ChatResponseReviewer: func(_ context.Context, agentName, baseAgent, userID, response string) string {
			gotAgent, gotBase, gotUser, gotResp = agentName, baseAgent, userID, response
			return "REDACTED"
		},
	}}
	got := s.reviewChatResponse(context.Background(), "concierge", "concierge", "guest", "his address is 123 Main St")
	if got != "REDACTED" {
		t.Errorf("reviewer rewrite must win; got %q", got)
	}
	if gotAgent != "concierge" || gotBase != "concierge" || gotUser != "guest" || gotResp != "his address is 123 Main St" {
		t.Errorf("reviewer got wrong args: %q %q %q %q", gotAgent, gotBase, gotUser, gotResp)
	}
}

func TestReviewChatResponse_EmptyReviewerOutputKeepsOriginal(t *testing.T) {
	s := &Server{cfg: Config{
		ChatResponseReviewer: func(_ context.Context, _, _, _, _ string) string { return "" },
	}}
	got := s.reviewChatResponse(context.Background(), "concierge", "concierge", "guest", "original")
	if got != "original" {
		t.Errorf("empty reviewer output must keep original (no-op); got %q", got)
	}
}

func TestReviewChatResponse_EmptyResponseNotReviewed(t *testing.T) {
	called := false
	s := &Server{cfg: Config{
		ChatResponseReviewer: func(_ context.Context, _, _, _, _ string) string { called = true; return "x" },
	}}
	got := s.reviewChatResponse(context.Background(), "concierge", "concierge", "guest", "")
	if called {
		t.Error("reviewer should not run on an empty response")
	}
	if got != "" {
		t.Errorf("empty response must stay empty; got %q", got)
	}
}
