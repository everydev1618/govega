package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func onboardingTestServer(t *testing.T, configuredURL, boundAddr string) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents: map[string]*dsl.Agent{
			"iris": {Name: "iris", Model: "claude-sonnet-4-6", IsMeta: true},
		},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	s := &Server{
		store:   newTestStore(t),
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
		cfg: Config{
			Version:      "v9.9.9-test",
			PublicURL:    configuredURL,
			Builder:      dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{Name: "iris"},
		},
	}
	s.initPublicURL(configuredURL, "", "8822", boundAddr)
	return s
}

// The wizard asks only when the answer cannot be deduced: the instance is
// bound beyond loopback (so localhost links are wrong for somebody) and
// nothing — config, a saved answer, or an observed browser origin — has told
// us the real name yet.
func TestOnboardingStatusNeedsPublicURL(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")

	got := getOnboarding(t, s)
	if !got.NeedsPublicURL {
		t.Errorf("NeedsPublicURL = false, want true on an exposed instance with no public URL")
	}
	if got.PublicURL != "http://localhost:8822" {
		t.Errorf("PublicURL = %q, want the localhost fallback", got.PublicURL)
	}
	if got.PublicURLSource != publicURLSourceFallback {
		t.Errorf("PublicURLSource = %q, want %q", got.PublicURLSource, publicURLSourceFallback)
	}
	if got.Completed {
		t.Errorf("Completed = true before anyone answered")
	}
}

// A laptop bound to loopback is correctly described by localhost. Never nag.
func TestOnboardingStatusQuietOnLoopback(t *testing.T) {
	s := onboardingTestServer(t, "", "127.0.0.1:8822")
	if getOnboarding(t, s).NeedsPublicURL {
		t.Errorf("NeedsPublicURL = true for a loopback-bound instance")
	}
}

// An operator who set PUBLIC_URL has already answered.
func TestOnboardingStatusQuietWhenConfigured(t *testing.T) {
	s := onboardingTestServer(t, "https://vega.example.com", "0.0.0.0:8822")
	got := getOnboarding(t, s)
	if got.NeedsPublicURL {
		t.Errorf("NeedsPublicURL = true despite PublicURL config")
	}
	if got.PublicURL != "https://vega.example.com" {
		t.Errorf("PublicURL = %q", got.PublicURL)
	}
}

// Answering the question persists it, applies it live to the layers that mint
// deliverable links, and stops the wizard asking again.
func TestOnboardingSetPublicURL(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")

	got := postOnboarding(t, s, map[string]any{"public_url": "vega.const"}, http.StatusOK)

	if got.PublicURL != "http://vega.const" {
		t.Fatalf("PublicURL = %q, want http://vega.const", got.PublicURL)
	}
	if got.PublicURLSource != publicURLSourceExplicit {
		t.Errorf("PublicURLSource = %q, want %q", got.PublicURLSource, publicURLSourceExplicit)
	}
	if got.NeedsPublicURL {
		t.Errorf("NeedsPublicURL still true after being answered")
	}
	if !got.Completed {
		t.Errorf("Completed = false after being answered")
	}

	// Persisted, so a restart doesn't re-ask.
	setting, err := s.store.GetSetting(settingKeyPublicURL)
	if err != nil || setting == nil {
		t.Fatalf("GetSetting(%q) = %v, %v", settingKeyPublicURL, setting, err)
	}
	if setting.Value != "http://vega.const" {
		t.Errorf("persisted value = %q", setting.Value)
	}

	// Applied live: new deliverable links use it immediately.
	if got := s.interp.Tools().BaseURL(); got != "http://vega.const" {
		t.Errorf("tools base URL = %q, want it applied live", got)
	}
}

func TestOnboardingRejectsBadPublicURL(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")
	for _, bad := range []string{"", "ftp://vega.const", "http://vega.const/with/path", "not a url"} {
		t.Run(bad, func(t *testing.T) {
			postOnboarding(t, s, map[string]any{"public_url": bad}, http.StatusBadRequest)
		})
	}
}

// "localhost really is how I reach it" is a valid answer. Dismissing marks
// onboarding complete without pinning a wrong URL.
func TestOnboardingDismiss(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")

	got := postOnboarding(t, s, map[string]any{"dismiss": true}, http.StatusOK)

	if !got.Completed {
		t.Errorf("Completed = false after dismiss")
	}
	if got.PublicURL != "http://localhost:8822" {
		t.Errorf("dismiss changed the URL to %q", got.PublicURL)
	}
	if setting, _ := s.store.GetSetting(settingKeyOnboardingCompleted); setting == nil {
		t.Errorf("dismiss not persisted")
	}
}

// The deduction path: a browser reaching us on a real hostname teaches the
// server its own name, with no question asked. The observation is persisted
// so Telegram and cron turns — which have no request to learn from — inherit
// it after a restart.
func TestObservePublicURLFromRequest(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")

	r := httptest.NewRequest("GET", "/api/v1/stats", nil)
	r.Host = "vega.const"
	s.observePublicURL(r)

	if got := s.publicURL.Base(); got != "http://vega.const" {
		t.Errorf("Base() = %q, want the observed origin", got)
	}
	if got := s.interp.Tools().BaseURL(); got != "http://vega.const" {
		t.Errorf("tools base URL = %q, want the observed origin applied", got)
	}
	setting, err := s.store.GetSetting(settingKeyPublicURLObserved)
	if err != nil || setting == nil || setting.Value != "http://vega.const" {
		t.Fatalf("observed origin not persisted: %v, %v", setting, err)
	}
	if getOnboarding(t, s).NeedsPublicURL {
		t.Errorf("NeedsPublicURL = true after the origin was deduced")
	}
}

// Only the SPA's own API calls teach us an origin. A /workspace/ link is
// exactly what a victim could be lured into opening against an attacker's
// hostname, and we will not learn our identity from that.
func TestObservePublicURLIgnoresNonAPIPaths(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")

	r := httptest.NewRequest("GET", "/workspace/report.html", nil)
	r.Host = "evil.example.com"
	s.observePublicURL(r)

	if got := s.publicURL.Base(); got != "http://localhost:8822" {
		t.Errorf("Base() = %q — learned an origin from a non-API path", got)
	}
}

// A saved answer outranks anything observed later, so opening the dashboard
// through a second hostname can't silently retarget deliverable links.
func TestObservePublicURLDoesNotOverrideExplicit(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")
	postOnboarding(t, s, map[string]any{"public_url": "http://vega.const"}, http.StatusOK)

	r := httptest.NewRequest("GET", "/api/v1/stats", nil)
	r.Host = "et-m1:8822"
	s.observePublicURL(r)

	if got := s.publicURL.Base(); got != "http://vega.const" {
		t.Errorf("Base() = %q, want the explicitly configured URL to win", got)
	}
}

// Restart path: both persisted layers are restored, explicit first.
func TestRestorePublicURLFromStore(t *testing.T) {
	s := onboardingTestServer(t, "", "0.0.0.0:8822")
	if err := s.store.UpsertSetting(Setting{Key: settingKeyPublicURLObserved, Value: "http://et-m1:8822"}); err != nil {
		t.Fatalf("seed observed: %v", err)
	}
	if err := s.store.UpsertSetting(Setting{Key: settingKeyPublicURL, Value: "http://vega.const"}); err != nil {
		t.Fatalf("seed explicit: %v", err)
	}

	s.restorePublicURL()

	if got := s.publicURL.Base(); got != "http://vega.const" {
		t.Errorf("Base() = %q after restore", got)
	}
	if got := s.publicURL.Source(); got != publicURLSourceExplicit {
		t.Errorf("Source() = %q after restore", got)
	}
}

func TestPublicURLSuggestions(t *testing.T) {
	got := publicURLSuggestions("et-m1", "8822", "")
	if len(got) == 0 || got[0] != "http://et-m1:8822" {
		t.Errorf("suggestions = %v, want the machine hostname first", got)
	}
	// A hostname that is just "localhost" teaches nothing.
	if got := publicURLSuggestions("localhost", "8822", ""); len(got) != 0 {
		t.Errorf("suggestions = %v, want none for a localhost hostname", got)
	}
	// An already-observed origin is the best suggestion of all.
	got = publicURLSuggestions("et-m1", "8822", "http://vega.const")
	if len(got) == 0 || got[0] != "http://vega.const" {
		t.Errorf("suggestions = %v, want the observed origin first", got)
	}
}

// --- helpers ---

func getOnboarding(t *testing.T, s *Server) OnboardingStatus {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleGetOnboarding(w, httptest.NewRequest("GET", "/api/v1/onboarding", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /onboarding = %d: %s", w.Code, w.Body.String())
	}
	var got OnboardingStatus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return got
}

func postOnboarding(t *testing.T, s *Server, body map[string]any, wantCode int) OnboardingStatus {
	t.Helper()
	buf, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	s.handleSetOnboarding(w, httptest.NewRequest("POST", "/api/v1/onboarding", bytes.NewReader(buf)))
	if w.Code != wantCode {
		t.Fatalf("POST /onboarding = %d, want %d: %s", w.Code, wantCode, w.Body.String())
	}
	var got OnboardingStatus
	if wantCode == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
	}
	return got
}

// Config.PublicURL beats the PUBLIC_URL env var: an embedder that wires the
// field explicitly means it, and shouldn't be overridden by whatever the
// process happens to inherit.
func TestInitPublicURLConfigBeatsEnv(t *testing.T) {
	cases := []struct {
		name, cfgURL, envURL, want, wantSource string
	}{
		{"config wins", "https://cfg.example.com/", "https://env.example.com", "https://cfg.example.com", publicURLSourceConfig},
		{"env fallback when config empty", "", "https://et.v39a.com/", "https://et.v39a.com", publicURLSourceConfig},
		{"localhost when both empty", "", "", "http://localhost:8822", publicURLSourceFallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := onboardingTestServer(t, "", "0.0.0.0:8822")
			s.initPublicURL(tc.cfgURL, tc.envURL, "8822", "0.0.0.0:8822")
			if got := s.publicURL.Base(); got != tc.want {
				t.Errorf("Base() = %q, want %q", got, tc.want)
			}
			if got := s.publicURL.Source(); got != tc.wantSource {
				t.Errorf("Source() = %q, want %q", got, tc.wantSource)
			}
		})
	}
}
