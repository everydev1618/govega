package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// tenantTestServer is the minimum a tenant-config test needs: store
// for settings, a real interpreter so the orchestrator rename path
// can re-inject Iris, broker for the SSE poke.
func tenantTestServer(t *testing.T) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents: map[string]*dsl.Agent{
			"aria": {Name: "aria", DisplayName: "ARIA", Title: "Orchestrator", Model: "claude-sonnet-4-6", IsMeta: true},
		},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	return &Server{
		store:   newTestStore(t),
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
		cfg: Config{
			Version: "v9.9.9-test",
			Builder: dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{
				Name: "aria", DisplayName: "ARIA", Title: "Orchestrator", ProductName: "Apex",
			},
		},
	}
}

// TestTenantConfig_GetReturnsCurrent pins the read shape — fields
// reflect cfg.Orchestrator (already overridden at boot from settings
// in handleStart's applyOrchestratorOverrides) plus branding pulled
// from the settings table.
func TestTenantConfig_GetReturnsCurrent(t *testing.T) {
	s := tenantTestServer(t)
	_ = s.store.UpsertSetting(Setting{Key: brandingAccentColorSettingKey, Value: "#3B82F6"})
	_ = s.store.UpsertSetting(Setting{Key: brandingLogoURLSettingKey, Value: "https://cdn/logo.svg"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/config", nil)
	w := httptest.NewRecorder()
	s.handleGetTenantConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got TenantConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OrchestratorName != "aria" || got.OrchestratorDisplay != "ARIA" || got.OrchestratorTitle != "Orchestrator" {
		t.Errorf("orchestrator fields = %+v", got)
	}
	if got.AccentColor != "#3B82F6" || got.LogoURL != "https://cdn/logo.svg" {
		t.Errorf("branding fields = %+v", got)
	}
	if got.ProductName != "Apex" {
		t.Errorf("product_name = %q", got.ProductName)
	}
	if got.Version != "v9.9.9-test" {
		t.Errorf("version = %q, want v9.9.9-test", got.Version)
	}
}

// TestTenantConfig_UpdateRoutesOrchestratorRename confirms a PUT that
// changes the orchestrator slug + display + title flows through the
// existing handleUpdateOrchestrator path: settings written, cfg
// mutated, Iris re-injected under the new identity.
func TestTenantConfig_UpdateRoutesOrchestratorRename(t *testing.T) {
	s := tenantTestServer(t)

	newName := "atlas"
	newDisplay := "Atlas"
	newTitle := "Chief of Staff"
	body := mustJSON(t, UpdateTenantConfigRequest{
		OrchestratorName:    &newName,
		OrchestratorDisplay: &newDisplay,
		OrchestratorTitle:   &newTitle,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tenant/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUpdateTenantConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// In-memory cfg mutated.
	if s.cfg.Orchestrator.Name != "atlas" {
		t.Errorf("cfg.Orchestrator.Name = %q, want atlas", s.cfg.Orchestrator.Name)
	}
	if s.cfg.Orchestrator.DisplayName != "Atlas" {
		t.Errorf("cfg.Orchestrator.DisplayName = %q, want Atlas", s.cfg.Orchestrator.DisplayName)
	}

	// Settings persisted (the boot-time apply will pick these up on restart).
	got, _ := s.store.GetSetting(orchestratorNameSettingKey)
	if got == nil || got.Value != "atlas" {
		t.Errorf("orchestrator.name setting = %+v", got)
	}

	// Interpreter has the new agent slug.
	if _, ok := s.interp.Document().Agents["atlas"]; !ok {
		t.Error("interpreter missing renamed orchestrator")
	}
}

// TestTenantConfig_UpdateBrandingOnly persists accent + logo and
// confirms the orchestrator side is untouched when only branding
// fields are supplied.
func TestTenantConfig_UpdateBrandingOnly(t *testing.T) {
	s := tenantTestServer(t)

	accent := "#EF4444"
	logo := "https://cdn/new-logo.png"
	body := mustJSON(t, UpdateTenantConfigRequest{
		AccentColor: &accent,
		LogoURL:     &logo,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tenant/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUpdateTenantConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if s.cfg.Orchestrator.Name != "aria" {
		t.Errorf("orchestrator name clobbered by branding-only PUT: %q", s.cfg.Orchestrator.Name)
	}
	got, _ := s.store.GetSetting(brandingAccentColorSettingKey)
	if got == nil || got.Value != "#EF4444" {
		t.Errorf("accent_color setting = %+v", got)
	}
	got, _ = s.store.GetSetting(brandingLogoURLSettingKey)
	if got == nil || got.Value != "https://cdn/new-logo.png" {
		t.Errorf("logo_url setting = %+v", got)
	}
}

// TestTenantConfig_UpdateBroadcastsSSE confirms a config change pokes
// the broker so the SPA refetches.
func TestTenantConfig_UpdateBroadcastsSSE(t *testing.T) {
	s := tenantTestServer(t)
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	accent := "#10B981"
	body := mustJSON(t, UpdateTenantConfigRequest{AccentColor: &accent})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tenant/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUpdateTenantConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var events []BrokerEvent
drain:
	for {
		select {
		case ev := <-sub:
			events = append(events, ev)
		default:
			break drain
		}
	}
	found := false
	for _, ev := range events {
		if ev.Type == "tenant.config_changed" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing tenant.config_changed event; got = %+v", events)
	}
}

// TestTenantConfig_UpdateRejectsBuilderCollision propagates the
// orchestrator-side guard: renaming to the builder slug returns 409.
func TestTenantConfig_UpdateRejectsBuilderCollision(t *testing.T) {
	s := tenantTestServer(t)
	collide := "hera"
	body := mustJSON(t, UpdateTenantConfigRequest{OrchestratorName: &collide})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tenant/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUpdateTenantConfig(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}
