package serve

import (
	"encoding/json"
	"net/http"
	"time"
)

// --- Tenant config — refs govega#32 item B ---
//
// Customer-facing branding + orchestrator identity, persisted in the
// settings table so the apex tenant binary can be rebranded at runtime
// without a restart or ops involvement. Replaces the env-var-only
// approach (APEX_ORCHESTRATOR_DISPLAY, accent color, logo URL) where
// the customer had to ping us to change anything.
//
// The orchestrator identity fields already round-trip through the
// settings table from #58's orchestrator-rename work — this handler
// just layers branding on top and serves both shapes from one endpoint
// so the FE has a single config surface to read/write.

// Setting keys for branding overrides. Live under a `branding.` prefix
// so they don't collide with feature-specific settings (mcp:*, gmail.*,
// orchestrator.*).
const (
	brandingAccentColorSettingKey = "branding.accent_color"
	brandingLogoURLSettingKey     = "branding.logo_url"
)

// TenantConfigResponse is the unified shape returned by
// GET /api/v1/tenant/config. Carries both the orchestrator identity
// (read off s.cfg.Orchestrator — the live values after settings
// overrides have been applied at boot) and the branding fields read
// directly from the settings table.
type TenantConfigResponse struct {
	OrchestratorName    string `json:"orchestrator_name"`
	OrchestratorDisplay string `json:"orchestrator_display"`
	OrchestratorTitle   string `json:"orchestrator_title"`
	ProductName         string `json:"product_name,omitempty"`
	AccentColor         string `json:"accent_color,omitempty"`
	LogoURL             string `json:"logo_url,omitempty"`
	// Version is the govega build version. Read-only — ignored on PUT.
	Version string `json:"version,omitempty"`
}

// UpdateTenantConfigRequest is the partial-PUT body. Pointer fields
// distinguish "omitted" (leave unchanged) from "set to empty". The
// orchestrator slug stays a pointer so the FE can rename without
// also bumping display/title.
type UpdateTenantConfigRequest struct {
	OrchestratorName    *string `json:"orchestrator_name,omitempty"`
	OrchestratorDisplay *string `json:"orchestrator_display,omitempty"`
	OrchestratorTitle   *string `json:"orchestrator_title,omitempty"`
	AccentColor         *string `json:"accent_color,omitempty"`
	LogoURL             *string `json:"logo_url,omitempty"`
}

// handleGetTenantConfig returns the current tenant identity + branding.
// GET /api/v1/tenant/config
func (s *Server) handleGetTenantConfig(w http.ResponseWriter, r *http.Request) {
	accent, _ := s.store.GetSetting(brandingAccentColorSettingKey)
	logo, _ := s.store.GetSetting(brandingLogoURLSettingKey)
	resp := TenantConfigResponse{
		OrchestratorName:    s.cfg.Orchestrator.Name,
		OrchestratorDisplay: s.cfg.Orchestrator.DisplayName,
		OrchestratorTitle:   s.cfg.Orchestrator.Title,
		ProductName:         s.cfg.Orchestrator.ProductName,
		Version:             s.cfg.Version,
	}
	if accent != nil {
		resp.AccentColor = accent.Value
	}
	if logo != nil {
		resp.LogoURL = logo.Value
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateTenantConfig is the customer-facing rename + rebrand path.
// PUT /api/v1/tenant/config
//
// Partial — omitted fields stay. Orchestrator fields go through the
// same persist + cfg-mutate + re-inject path as
// PUT /api/v1/agents/{name} so the rename takes effect immediately.
// Branding fields just persist as settings and broadcast a
// tenant.config_changed event so the SPA refetches identity.
//
// admin-only when auth lands (#32 item A); for now open like every
// other PUT in serve.
func (s *Server) handleUpdateTenantConfig(w http.ResponseWriter, r *http.Request) {
	var req UpdateTenantConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	// Orchestrator-side: if any of the three identity fields changed,
	// route through the existing handleUpdateOrchestrator flow so the
	// rename re-injects Iris properly. We map this UpdateTenantConfig
	// shape into the UpdateAgent shape the orchestrator handler expects.
	if req.OrchestratorName != nil || req.OrchestratorDisplay != nil || req.OrchestratorTitle != nil {
		oldName := s.cfg.Orchestrator.Name
		agentReq := UpdateAgentRequest{
			Name:        req.OrchestratorName,
			DisplayName: req.OrchestratorDisplay,
			Title:       req.OrchestratorTitle,
		}
		// handleUpdateOrchestrator writes the response itself; capture
		// it so we can fall through to the branding step on success.
		rec := newResponseRecorder()
		s.handleUpdateOrchestrator(rec, oldName, agentReq)
		if rec.code >= 300 {
			rec.copyTo(w)
			return
		}
		// Discard the orchestrator-specific 200 body — we'll emit the
		// unified config response below after branding lands.
	}

	// Branding side: just settings + an SSE poke.
	if req.AccentColor != nil {
		if err := s.store.UpsertSetting(Setting{Key: brandingAccentColorSettingKey, Value: *req.AccentColor}); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to persist accent_color: " + err.Error()})
			return
		}
	}
	if req.LogoURL != nil {
		if err := s.store.UpsertSetting(Setting{Key: brandingLogoURLSettingKey, Value: *req.LogoURL}); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to persist logo_url: " + err.Error()})
			return
		}
	}

	// Broadcast so the SPA / any open identity-aware UI refetches.
	s.broker.Publish(BrokerEvent{
		Type:      "tenant.config_changed",
		Timestamp: time.Now(),
	})

	// Echo the new state — same shape as GET — so the caller doesn't
	// need a follow-up round trip.
	accent, _ := s.store.GetSetting(brandingAccentColorSettingKey)
	logo, _ := s.store.GetSetting(brandingLogoURLSettingKey)
	resp := TenantConfigResponse{
		OrchestratorName:    s.cfg.Orchestrator.Name,
		OrchestratorDisplay: s.cfg.Orchestrator.DisplayName,
		OrchestratorTitle:   s.cfg.Orchestrator.Title,
		ProductName:         s.cfg.Orchestrator.ProductName,
		Version:             s.cfg.Version,
	}
	if accent != nil {
		resp.AccentColor = accent.Value
	}
	if logo != nil {
		resp.LogoURL = logo.Value
	}
	writeJSON(w, http.StatusOK, resp)
}

// responseRecorder is a thin http.ResponseWriter we can hand to
// handleUpdateOrchestrator and inspect afterwards, since that handler
// writes its own response. We reuse the captured body only on error
// (forwarded to the real writer) so the caller gets the existing
// orchestrator-specific error shape unchanged.
type responseRecorder struct {
	code    int
	headers http.Header
	body    []byte
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{code: http.StatusOK, headers: http.Header{}}
}
func (r *responseRecorder) Header() http.Header  { return r.headers }
func (r *responseRecorder) WriteHeader(code int) { r.code = code }
func (r *responseRecorder) Write(p []byte) (int, error) {
	r.body = append(r.body, p...)
	return len(p), nil
}
func (r *responseRecorder) copyTo(w http.ResponseWriter) {
	for k, vs := range r.headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(r.code)
	_, _ = w.Write(r.body)
}
