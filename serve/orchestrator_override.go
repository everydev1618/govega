package serve

import (
	"log/slog"
	"net/http"

	"github.com/everydev1618/govega/dsl"
)

// Settings keys for the orchestrator identity override (refs govega#58).
// The orchestrator is injected programmatically from cfg.Orchestrator on
// every boot, so persisting a rename to composed_agents would just
// duplicate it next start. We persist the override here instead, and the
// boot path applies it to cfg before injectIris.
const (
	orchestratorNameSettingKey        = "orchestrator.name"
	orchestratorDisplayNameSettingKey = "orchestrator.display_name"
	orchestratorTitleSettingKey       = "orchestrator.title"
)

// orchestratorOverrideStore is the slice of store the override flow needs.
// Kept narrow so tests can stub it without dragging in the full Store.
type orchestratorOverrideStore interface {
	GetSetting(key string) (*Setting, error)
	UpsertSetting(s Setting) error
}

// applyOrchestratorOverrides returns cfg with any persisted identity
// override settings applied. Returns cfg unchanged when no overrides are
// present. Called from Server.Start before injectIris so the orchestrator
// boots under its renamed identity (refs govega#58).
func applyOrchestratorOverrides(cfg dsl.IrisConfig, store orchestratorOverrideStore) dsl.IrisConfig {
	if name, err := store.GetSetting(orchestratorNameSettingKey); err == nil && name != nil && name.Value != "" {
		cfg.Name = name.Value
	}
	if dn, err := store.GetSetting(orchestratorDisplayNameSettingKey); err == nil && dn != nil && dn.Value != "" {
		cfg.DisplayName = dn.Value
	}
	if title, err := store.GetSetting(orchestratorTitleSettingKey); err == nil && title != nil && title.Value != "" {
		cfg.Title = title.Value
	}
	return cfg
}

// persistOrchestratorOverrides writes the supplied identity fields to the
// settings table. Empty values are skipped (callers can omit a field by
// passing "") so a PUT that only changes the display name doesn't blank
// out the slug.
func persistOrchestratorOverrides(store orchestratorOverrideStore, name, displayName, title string) error {
	if name != "" {
		if err := store.UpsertSetting(Setting{Key: orchestratorNameSettingKey, Value: name}); err != nil {
			return err
		}
	}
	if displayName != "" {
		if err := store.UpsertSetting(Setting{Key: orchestratorDisplayNameSettingKey, Value: displayName}); err != nil {
			return err
		}
	}
	if title != "" {
		if err := store.UpsertSetting(Setting{Key: orchestratorTitleSettingKey, Value: title}); err != nil {
			return err
		}
	}
	return nil
}

// renameOrchestrator handles the rename side-effects: removes the old agent
// from the interpreter and re-injects with the updated cfg. Logs warnings
// rather than returning errors — the persistence step has already
// succeeded by the time this runs, so a partial in-memory state is
// recoverable on next restart.
func (s *Server) renameOrchestrator(oldName string) {
	if err := s.interp.RemoveAgent(oldName); err != nil {
		slog.Warn("orchestrator rename: failed to remove old agent", "old", oldName, "error", err)
	}
	s.injectIris()
}

// handleUpdateOrchestrator services PUT /api/v1/agents/{name} when the
// target is the currently-configured orchestrator. It persists the
// identity change as settings, mutates s.cfg.Orchestrator in-place so
// every cfg.Orchestrator.Name read across the server picks up the rename
// immediately, and re-injects Iris under the new identity.
//
// Only `name`, `display_name`, and `title` are honored — these are the
// fields the FE's onboarding step writes. Other UpdateAgentRequest fields
// are ignored on the meta-agent because the orchestrator's system prompt,
// model, and tool list are owned by IrisConfig (and shouldn't be edited
// per-tenant via the UI).
func (s *Server) handleUpdateOrchestrator(w http.ResponseWriter, oldName string, req UpdateAgentRequest) {
	newName := oldName
	if req.Name != nil && *req.Name != "" {
		newName = *req.Name
	}
	if newName != oldName && newName == s.cfg.Builder.Name {
		writeJSON(w, http.StatusConflict, ErrorResponse{Error: "orchestrator name conflicts with builder"})
		return
	}

	newDisplay := s.cfg.Orchestrator.DisplayName
	if req.DisplayName != nil && *req.DisplayName != "" {
		newDisplay = *req.DisplayName
	}
	newTitle := s.cfg.Orchestrator.Title
	if req.Title != nil && *req.Title != "" {
		newTitle = *req.Title
	}

	// Persist BEFORE mutating in-memory state so a crash mid-rename leaves
	// the DB authoritative and a restart resolves to the new identity.
	if err := persistOrchestratorOverrides(s.store, newName, newDisplay, newTitle); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to persist orchestrator override: " + err.Error()})
		return
	}

	s.cfg.Orchestrator.Name = newName
	s.cfg.Orchestrator.DisplayName = newDisplay
	s.cfg.Orchestrator.Title = newTitle

	s.renameOrchestrator(oldName)

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "name": newName})
}
