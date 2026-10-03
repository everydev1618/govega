// Package serve — onboarding.go
//
// First-run setup. The principle is that onboarding asks only what it cannot
// work out for itself: a browser reaching the dashboard on a real hostname
// already tells the server its public name, so the only instance that sees a
// question is one bound beyond loopback that nobody has yet reached by any
// name but localhost.
//
// Today that is one question — the base URL agents put in deliverable links.
// The status payload is shaped to grow: the wizard renders whatever steps the
// server says are outstanding.

package serve

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/everydev1618/govega/tools"
)

// OnboardingStatus is what the first-run wizard renders.
type OnboardingStatus struct {
	// Completed is set once the operator has answered or dismissed.
	Completed bool `json:"completed"`
	// NeedsPublicURL is true when deliverable links would otherwise go out
	// as localhost on an instance reachable by some other name.
	NeedsPublicURL bool `json:"needs_public_url"`
	// PublicURL is the base URL in force right now.
	PublicURL string `json:"public_url"`
	// PublicURLSource names the layer it came from: config, explicit,
	// observed, or fallback.
	PublicURLSource string `json:"public_url_source"`
	// Suggestions are plausible answers for the form.
	Suggestions []string `json:"suggestions,omitempty"`
}

type onboardingRequest struct {
	PublicURL string `json:"public_url"`
	Dismiss   bool   `json:"dismiss"`
}

// initPublicURL builds the resolver and wires persistence. Called from Start
// once the listener has resolved its address.
func (s *Server) initPublicURL(cfgURL, envURL, port, addr string) {
	configured := strings.TrimRight(strings.TrimSpace(cfgURL), "/")
	if configured == "" {
		configured = strings.TrimRight(strings.TrimSpace(envURL), "/")
	}

	s.publicPort = port
	s.publicURL = newPublicURLResolver(configured, "http://localhost:"+port)
	s.publicURL.SetExposed(addrIsExposed(addr))

	s.publicURL.OnChange(func(origin string) {
		s.persistSetting(settingKeyPublicURLObserved, origin)
		s.applyPublicURL()
		slog.Info("public URL learned from request origin", "url", origin)
	})
	s.publicURL.OnExplicitChange(func(u string) {
		s.persistSetting(settingKeyPublicURL, u)
		s.applyPublicURL()
		slog.Info("public URL set", "url", u)
	})
}

// restorePublicURL reloads the persisted layers at boot, so a learned or
// answered URL survives a restart — and so Telegram and cron turns, which
// have no request to learn from, start out correct.
func (s *Server) restorePublicURL() {
	if s.store == nil || s.publicURL == nil {
		return
	}
	var explicit, observed string
	if st, err := s.store.GetSetting(settingKeyPublicURL); err == nil && st != nil {
		explicit = st.Value
	}
	if st, err := s.store.GetSetting(settingKeyPublicURLObserved); err == nil && st != nil {
		observed = st.Value
	}
	s.publicURL.Restore(explicit, observed)
	s.applyPublicURL()
}

// applyPublicURL pushes the resolved base URL to the layers that mint links.
// Note that an agent's system prompt is built at spawn, so a URL learned
// later reaches already-running agents through tool results rather than
// through their prompt — which is the authoritative path anyway.
func (s *Server) applyPublicURL() {
	if s.publicURL == nil {
		return
	}
	base := s.publicURL.Base()
	if s.interp != nil {
		s.interp.SetServerBaseURL(base)
	}
	if s.localHost != nil {
		s.localHost.SetBaseURL(base)
	}
}

func (s *Server) persistSetting(key, value string) {
	if s.store == nil {
		return
	}
	if err := s.store.UpsertSetting(Setting{Key: key, Value: value}); err != nil {
		slog.Error("failed to persist setting", "key", key, "error", err)
	}
}

// observePublicURL learns this server's public name from a request.
//
// Only the SPA's own API calls count. A /workspace/ or /apps/ link is exactly
// what someone could be lured into opening against an attacker-chosen
// hostname, and we will not learn our own identity from that. Where auth is
// enabled, the request must also have passed it.
func (s *Server) observePublicURL(r *http.Request) {
	if s.publicURL == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		return
	}
	if s.authCfg.TenantID != "" {
		if _, ok := ClaimsFrom(r.Context()); !ok {
			return
		}
	}
	s.publicURL.Observe(originFromRequest(r))
}

// publicURLMiddleware learns the origin from each API request and attaches it
// to the context, so tools that mint deliverable links during this turn use
// the name the user actually reached us by rather than the boot-time guess.
func (s *Server) publicURLMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.observePublicURL(r)
		if origin := originFromRequest(r); origin != "" {
			r = r.WithContext(tools.ContextWithBaseURL(r.Context(), origin))
		}
		next.ServeHTTP(w, r)
	})
}

// currentPublicBase returns the base URL in force, or "" if not yet wired.
func (s *Server) currentPublicBase() string {
	if s.publicURL == nil {
		return ""
	}
	return s.publicURL.Base()
}

func (s *Server) onboardingStatus() OnboardingStatus {
	st := OnboardingStatus{
		PublicURL:       s.publicURL.Base(),
		PublicURLSource: s.publicURL.Source(),
		NeedsPublicURL:  s.publicURL.NeedsSetup(),
	}
	if s.store != nil {
		if row, err := s.store.GetSetting(settingKeyOnboardingCompleted); err == nil && row != nil && row.Value == "1" {
			st.Completed = true
		}
	}
	if !st.NeedsPublicURL {
		st.Completed = true
	}
	if st.NeedsPublicURL {
		hostname, _ := os.Hostname()
		st.Suggestions = publicURLSuggestions(hostname, s.publicPort, "")
	}
	return st
}

func (s *Server) handleGetOnboarding(w http.ResponseWriter, r *http.Request) {
	if s.publicURL == nil {
		writeJSON(w, http.StatusOK, OnboardingStatus{Completed: true})
		return
	}
	writeJSON(w, http.StatusOK, s.onboardingStatus())
}

func (s *Server) handleSetOnboarding(w http.ResponseWriter, r *http.Request) {
	if s.publicURL == nil {
		writeJSON(w, http.StatusOK, OnboardingStatus{Completed: true})
		return
	}

	var req onboardingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	switch {
	case strings.TrimSpace(req.PublicURL) != "":
		normalized, err := normalizePublicURL(req.PublicURL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}
		s.publicURL.SetExplicit(normalized)
	case req.Dismiss:
		// "localhost really is how I reach it" is a valid answer: mark
		// onboarding done without pinning a URL we'd then have to be wrong
		// about.
	default:
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "public_url is required"})
		return
	}

	s.persistSetting(settingKeyOnboardingCompleted, "1")

	st := s.onboardingStatus()
	st.Completed = true
	writeJSON(w, http.StatusOK, st)
}
