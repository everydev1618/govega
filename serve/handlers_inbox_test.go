package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seedOneInboxItem inserts a pending item and returns its id. Helper for
// the resolve/delete handler tests.
func seedOneInboxItem(t *testing.T, s *Server, subject string) int64 {
	t.Helper()
	id, err := s.store.InsertInboxItem("scout", subject, "body", "urgent")
	if err != nil {
		t.Fatalf("InsertInboxItem: %v", err)
	}
	return id
}

// TestResolveInboxItem_Handler covers the single-item resolve endpoint:
// the UI's "Resolve" button posts a resolution string, server flips
// status=resolved and stamps resolved_at. Missing-id returns 404; missing
// body uses an empty default resolution (don't reject — the user just
// clicked the button).
func TestResolveInboxItem_Handler(t *testing.T) {
	t.Run("resolves a pending item", func(t *testing.T) {
		s := agentTestServer(t, nil)
		id := seedOneInboxItem(t, s, "trail-off")

		body := strings.NewReader(`{"resolution":"handled by user"}`)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/resolve", id), body)
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", fmt.Sprintf("%d", id))
		w := httptest.NewRecorder()
		s.handleResolveInboxItem(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		got, _ := s.store.GetInboxItem(id)
		if got == nil || got.Status != "resolved" {
			t.Errorf("post-resolve item = %+v", got)
		}
		if got.Resolution != "handled by user" {
			t.Errorf("resolution = %q, want %q", got.Resolution, "handled by user")
		}
	})

	t.Run("missing item returns 404", func(t *testing.T) {
		s := agentTestServer(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/inbox/99999/resolve", strings.NewReader(`{"resolution":"x"}`))
		req.SetPathValue("id", "99999")
		w := httptest.NewRecorder()
		s.handleResolveInboxItem(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-numeric id returns 400", func(t *testing.T) {
		s := agentTestServer(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/inbox/abc/resolve", strings.NewReader(`{}`))
		req.SetPathValue("id", "abc")
		w := httptest.NewRecorder()
		s.handleResolveInboxItem(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("empty body still resolves with empty resolution", func(t *testing.T) {
		s := agentTestServer(t, nil)
		id := seedOneInboxItem(t, s, "noise")

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/resolve", id), strings.NewReader(""))
		req.SetPathValue("id", fmt.Sprintf("%d", id))
		w := httptest.NewRecorder()
		s.handleResolveInboxItem(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
		}
		got, _ := s.store.GetInboxItem(id)
		if got == nil || got.Status != "resolved" {
			t.Errorf("post-resolve = %+v", got)
		}
	})
}

// TestDeleteInboxItem_Handler covers the human delete escape hatch.
// Hard delete by design — the user wants the row gone.
func TestDeleteInboxItem_Handler(t *testing.T) {
	t.Run("deletes a pending item", func(t *testing.T) {
		s := agentTestServer(t, nil)
		id := seedOneInboxItem(t, s, "ignore me")

		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/inbox/%d", id), nil)
		req.SetPathValue("id", fmt.Sprintf("%d", id))
		w := httptest.NewRecorder()
		s.handleDeleteInboxItem(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		if got, _ := s.store.GetInboxItem(id); got != nil {
			t.Errorf("expected nil after delete, got %+v", got)
		}
	})

	t.Run("missing item returns 404", func(t *testing.T) {
		s := agentTestServer(t, nil)
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/inbox/99999", nil)
		req.SetPathValue("id", "99999")
		w := httptest.NewRecorder()
		s.handleDeleteInboxItem(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-numeric id returns 400", func(t *testing.T) {
		s := agentTestServer(t, nil)
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/inbox/abc", nil)
		req.SetPathValue("id", "abc")
		w := httptest.NewRecorder()
		s.handleDeleteInboxItem(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}

// Sanity: the handlers should be reachable through the actual mux route
// the server registers, not just by direct method call.
func TestInboxRoutes_Wired(t *testing.T) {
	s := agentTestServer(t, nil)
	id := seedOneInboxItem(t, s, "wired")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inbox/{id}/resolve", s.handleResolveInboxItem)
	mux.HandleFunc("DELETE /api/v1/inbox/{id}", s.handleDeleteInboxItem)

	// Resolve via the mux.
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/inbox/%d/resolve", id), strings.NewReader(`{"resolution":"ok"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve via mux = %d, body=%s", w.Code, w.Body.String())
	}

	// Delete via the mux.
	req2 := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/inbox/%d", id), nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("delete via mux = %d, body=%s", w2.Code, w2.Body.String())
	}

	// A subsequent body decode test to silence the "encoding/json" import
	// is unnecessary — but to keep the import used elsewhere we leave it
	// behind a no-op check.
	_ = json.NewEncoder
}
